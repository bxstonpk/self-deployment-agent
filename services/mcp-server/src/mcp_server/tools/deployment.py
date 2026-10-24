"""Section 13.6-13.8, 13.11-13.12: deploy_application, get_application_status,
get_deployment_status, rollback_application, restart_application.
"""

from __future__ import annotations

import base64
import binascii
from typing import Any

import yaml

from ..review import ReviewError, read_review_document
from ..envelope import ErrorCode, ToolError, pending_approval, success
from ..source_packer import PackError, pack_project
from ..idempotency import IdempotencyConflict, IdempotencyStore
from ..platform_client import PlatformClient

# What happens to an application whose first build or deployment fails
# (the platform owner, 2026-10-24).
_NEVER_RAN_NOTE = (
    " If this application has never run successfully, the platform removes it within a minute and frees its "
    "name: tell the employee, fix the problem, then start it again under the same name (start_project) and "
    "deploy. Nothing of it was ever live, so no data is lost."
)

_NO_BUILD_MESSAGE = (
    "no successful build exists for this application, and no source was given to "
    "build one. Pass project_path — the absolute path of the project folder on this "
    "machine, with one sub-folder per service named in deployment.yaml — and this "
    "tool packs and builds it."
)


async def _service_names(client: PlatformClient, application_id: str) -> list[str]:
    """The services the application's saved deployment.yaml declares — the
    folders the build expects, and the only ones packed."""
    app = await client.get_application(application_id)
    draft = app.get("deployment_yaml_draft") or ""
    try:
        parsed = yaml.safe_load(draft) or {}
    except yaml.YAMLError as exc:
        raise ToolError(ErrorCode.VALIDATION_ERROR, f"the saved deployment.yaml doesn't parse: {exc}") from exc
    services = parsed.get("services") if isinstance(parsed, dict) else None
    if not isinstance(services, dict) or not services:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "the application has no saved deployment.yaml with services; run validate_application first",
        )
    return [str(name) for name in services]


def _service_urls(deployment: dict[str, Any]) -> dict[str, str]:
    """Every service's own address, by service name. A single `url` used to
    be all an agent got — the first service's — which left it unable to
    tell the employee where the frontend was without guessing (found by a
    real Claude Code session deploying the LAN templates)."""
    containers = deployment.get("containers") or {}
    return {name: c["url"] for name, c in sorted(containers.items()) if isinstance(c, dict) and c.get("url")}


def _primary_url(urls: dict[str, str]) -> str | None:
    """The one address to give a person first: the frontend, if any."""
    if "frontend" in urls:
        return urls["frontend"]
    return next(iter(urls.values()), None)


_NO_REVIEW_MESSAGE = (
    "deployments to {env} wait for the approver, who reviews every file before signing — write a REVIEW.md "
    "in the project root first, then deploy again. It must cover: what the application does and who uses it; "
    "the workflow (how a request moves through frontend, API and database); the structure, with the path and "
    "purpose of every file you wrote or changed; the data it stores (tables, columns); anything it calls outside "
    "itself; the configuration and secret NAMES it reads (never values); and what changed since the last "
    "deployment. Nothing was built."
)


_ASK_ABOUT_DEV_DATA = (
    "Before deploying to production, ASK the employee whether the data in the dev copy ({name}) is real data "
    "to keep. {test_data}Production's first deployment can start with that data (dev_data=\"copy\": the "
    "approver sees and signs that choice, and the data is copied once production is live) or empty "
    "(dev_data=\"leave\"). This happens only once: after that, production's data is its own. Then call "
    "deploy_application again with their answer. Nothing was built."
)


async def _dev_data_choice(client: PlatformClient, application_id: str, dev_data: str | None) -> bool:
    """Whether this production deployment takes the dev copy's data. Asks
    (through an error) when there is a choice to make and none was given."""
    copy = await client.get_dev_copy(application_id)
    if not copy:
        if dev_data == "copy":
            raise ToolError(ErrorCode.VALIDATION_ERROR, "there is no dev copy to take data from")
        return False
    history = await client.deployment_history(application_id)
    production_ran = any(
        d.get("environment") == "production" and d.get("status") in ("running", "superseded") for d in history
    )
    if production_ran:
        if dev_data == "copy":
            raise ToolError(
                ErrorCode.VALIDATION_ERROR,
                "production has already run and has its own data: dev data is copied only by the first "
                "production deployment, so real data is never replaced",
            )
        return False
    if dev_data is None:
        test_data = (
            f"You (Claude) recorded test data in it: {copy.get('test_data_note')}. Say so: that data must not "
            "go to production unless the employee says it is real. "
            if copy.get("test_data") else
            "No test data was recorded as added by Claude, but ask anyway. "
        )
        raise ToolError(ErrorCode.VALIDATION_ERROR,
                        _ASK_ABOUT_DEV_DATA.format(name=copy.get("name"), test_data=test_data))
    return dev_data == "copy"


async def _deploy_dev_copy(
    client: PlatformClient, application_id: str, source: bytes, test_run_id: str | None, packed: dict[str, Any] | None,
) -> dict[str, Any]:
    res = await client.deploy_dev(application_id, source)
    copy = res.get("dev_copy", {})
    build = res.get("build", {})
    deployment = res.get("deployment") or {}
    if build.get("status") == "failed":
        detail = (build.get("error_detail") or "no detail").removeprefix("build failed: ")
        code = ErrorCode.VALIDATION_ERROR if build.get("error_category") == "source" else ErrorCode.INTERNAL_ERROR
        raise ToolError(code, (
            f"the dev copy didn't build: {detail}. If the dev copy has run before, its running version and data "
            "are as they were. If it never ran, the platform removes it within a minute; deploying to dev again "
            "makes a new one at the same address."
        ))
    data: dict[str, Any] = {
        "application_id": application_id,
        "environment": "dev",
        "dev_copy": copy,
        "url": copy.get("url"),
        "status": deployment.get("status"),
        "deployment_id": deployment.get("id"),
        "created": res.get("created", False),
        "note": (
            f"Deployed to the dev copy {copy.get('name')}: its own application, database and address, never "
            "waiting for approval. Its data stays between deployments. Its pages carry a DEV banner. The "
            "application itself (production) is untouched."
        ),
        "tell_the_employee": (
            "Give the employee the url now: this is their application running, the way to see and use it (not a "
            "local file or a local dev server). It is the dev copy, for trying; it stays, with its data, and "
            "after each change you deploy to dev again and it's at the same url. Production is a separate step "
            "they ask for when they're ready; mention that once, don't push it. If you put any data into it "
            "yourself to test, call record_test_data now."
        ),
    }
    if test_run_id:
        data["test_run_id"] = test_run_id
    if packed is not None:
        data["source"] = packed
    if deployment.get("status") not in (None, "running"):
        data["deployment_note"] = (
            f"the dev deployment is {deployment.get('status')}: {deployment.get('failure_reason') or ''} "
            "Read its logs with get_application_logs on the dev copy's application_id."
        )
    return success(data)


def _report_tail(report: str, lines: int = 60) -> str:
    """The end of a test report, where failures and summaries are."""
    kept = report.rstrip("\n").split("\n")
    if len(kept) <= lines:
        return "\n".join(kept)
    return "(earlier lines: run_tests returns the whole report)\n" + "\n".join(kept[-lines:])


async def deploy_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    target_environment: str,
    version_reference: str | None = None,
    source_archive_base64: str | None = None,
    idempotency_key: str | None = None,
    project_path: str | None = None,
    test_report: str | None = None,
    review_document: str | None = None,
    run_tests: bool | None = None,
    test_run_id: str | None = None,
    dev_data: str | None = None,
) -> dict[str, Any]:
    # version_reference is accepted for shape-compliance with Section 13.6
    # but not enforced: this Platform API always deploys the application's
    # LATEST successful build — there is no "deploy this specific older
    # build" path (that's rollback_application, which takes an explicit
    # target). Silently accepting and ignoring it would hide the gap;
    # silently honoring only "latest"/None would be surprising if the
    # caller expected real version pinning. Documented here instead.
    #
    # source_archive_base64 is NOT part of Section 13.6's declared input
    # shape, but closes the real gap documented in this module's git
    # history: without it, there was no way for deploy_application to
    # actually trigger Build the way its own Purpose text describes
    # ("Build -> Image Scan -> Deploy -> Health Check -> Traffic
    # Activation"). When given, this tool calls the Platform API's build
    # endpoint first — which now accepts Validated (first build) OR
    # Running/Failed (a rebuild) as of the fix alongside this one — then
    # proceeds to deploy only if the build succeeded.
    #
    # project_path is the normal way to give the source: the folder on this
    # machine, packed here by source_packer (see its module doc for what is
    # never included). source_archive_base64 remains for a caller that has
    # no filesystem access to the project.
    if project_path and source_archive_base64:
        raise ToolError(ErrorCode.VALIDATION_ERROR, "pass project_path or source_archive_base64, not both")
    fingerprint = (application_id, target_environment, version_reference, source_archive_base64, project_path,
                   run_tests, test_run_id, dev_data)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    # Dev deployments go to the application's dev copy (DEC-029 part 3):
    # its own database and address, never waiting for approval.
    policy = await client.get_policy()
    to_dev_copy = target_environment == "dev" and bool((policy or {}).get("dev_copies"))
    # Dev is the employee's running application to look at, redeployed after
    # every change: fast, so the tests run only when asked (the build still
    # compiles everything). Production always runs them, unless told not to.
    if run_tests is None:
        run_tests = not to_dev_copy
    if dev_data not in (None, "copy", "leave"):
        raise ToolError(ErrorCode.VALIDATION_ERROR, 'dev_data is "copy" or "leave"')
    copy_dev_data = False
    if target_environment == "production" and (policy or {}).get("dev_copies"):
        copy_dev_data = await _dev_data_choice(client, application_id, dev_data)

    # REVIEW.md: required for an environment that waits for approval, and
    # checked before building so a missing one costs nothing.
    if project_path:
        try:
            found = read_review_document(project_path)
        except ReviewError as exc:
            raise ToolError(ErrorCode.VALIDATION_ERROR, str(exc)) from exc
        review_document = found or review_document
    if not (review_document or "").strip() and not to_dev_copy:
        gated = (policy or {}).get("approval_required_environments", ["production"]) if policy is not None else ["production"]
        if target_environment in gated:
            raise ToolError(ErrorCode.VALIDATION_ERROR, _NO_REVIEW_MESSAGE.format(env=target_environment))

    source_bytes: bytes | None = None
    packed: dict[str, Any] | None = None
    if project_path:
        try:
            source_bytes, summary = pack_project(project_path, await _service_names(client, application_id))
        except PackError as exc:
            raise ToolError(ErrorCode.VALIDATION_ERROR, str(exc)) from exc
        packed = summary.as_dict()
    elif source_archive_base64:
        try:
            source_bytes = base64.b64decode(source_archive_base64, validate=True)
        except (binascii.Error, ValueError) as exc:
            raise ToolError(
                ErrorCode.VALIDATION_ERROR, f"source_archive_base64 is not valid base64: {exc}"
            ) from exc
    # The platform runs the tests on this exact source before building it,
    # and the deployment carries the platform's report (DEC-029 part 2).
    tests: dict[str, Any] | None = None
    if source_bytes is not None and run_tests and not test_run_id:
        tests = await client.run_tests(application_id, source_bytes)
        if tests.get("status") != "passed":
            raise ToolError(
                ErrorCode.VALIDATION_ERROR,
                f"the tests didn't pass on the platform ({tests.get('status')}); nothing was built.\n"
                + _report_tail(tests.get("report", "")),
            )
        test_run_id = tests.get("id")
    if to_dev_copy:
        if source_bytes is None:
            raise ToolError(ErrorCode.VALIDATION_ERROR, "a dev deployment needs the source: pass project_path")
        result = await _deploy_dev_copy(client, application_id, source_bytes, test_run_id, packed)
        if idempotency_key:
            idempotency.store(idempotency_key, fingerprint, result)
        return result
    if source_bytes is not None:
        build = await client.trigger_build(application_id, source_bytes)
        if build.get("status") == "failed":
            category = build.get("error_category")
            detail = build.get("error_detail") or "build failed for an unspecified reason"
            # The platform's detail already starts "build failed: " (domain.BuildFailure).
            detail = detail.removeprefix("build failed: ")
            if category == "source":
                # FR-038: a source-category failure (compiler/dependency
                # error) is the employee's/agent's problem to fix, not the
                # platform's — VALIDATION_ERROR, not INTERNAL_ERROR.
                raise ToolError(ErrorCode.VALIDATION_ERROR, f"build failed: {detail}." + _NEVER_RAN_NOTE)
            raise ToolError(ErrorCode.INTERNAL_ERROR, f"build failed (platform-side): {detail}." + _NEVER_RAN_NOTE)

    try:
        deployment = await client.deploy_application(
            application_id, target_environment, test_report, review_document, test_run_id, copy_dev_data
        )
    except ToolError as exc:
        if exc.platform_code == "no_successful_build":
            raise ToolError(ErrorCode.VALIDATION_ERROR, _NO_BUILD_MESSAGE) from exc
        raise

    status = deployment.get("status")
    # This Platform API's deploy pipeline runs synchronously within the
    # request (see platform-api/README.md's "What's deliberately NOT here
    # yet" — no background job/worker model). Section 9 wants an
    # IMMEDIATE ack with a queued/building phase and separate polling; here
    # the "ack" and the terminal result are the same call, because there is
    # no queue to ack into. get_deployment_status still works correctly
    # afterward (the deployment record is always queryable regardless of
    # how it got there) — only the "return before completion" property is
    # not real yet.
    data = {
        "deployment_id": deployment.get("id"),
        "application_id": application_id,
        "status": status,
        "note": (
            "this call already ran the full pipeline synchronously and "
            "returned its terminal result — see this tool's module doc "
            "comment for why 'immediate async ack' isn't real here yet"
        ),
    }
    if status == "running":
        data["urls"] = _service_urls(deployment)
        if copy_dev_data:
            if deployment.get("dev_data_copied_at"):
                data["dev_data"] = "copied from the dev copy into production's database"
            else:
                data["dev_data"] = (
                    "NOT copied: " + (deployment.get("dev_data_copy_error") or "no reason given")
                    + ". Production is running with an empty database; tell the employee."
                )
    if test_run_id:
        data["test_run_id"] = test_run_id
        data["tests"] = "run by the platform; its report is attached to this deployment"
        if tests is not None:
            data["test_services"] = tests.get("services", [])
    if packed is not None:
        data["source"] = packed
        if packed["skipped_secret_files"]:
            data["source_note"] = (
                "These files were NOT uploaded because they look like local secrets: "
                + ", ".join(packed["skipped_secret_files"])
                + ". If the application needs those values, the employee sets them as secrets "
                "in the Admin Portal (Secrets section); never copy them into source."
            )

    if status == "pending_approval":
        data["approval_note"] = (
            "This deployment waits for a platform administrator's signed approval. They review the "
            "images, the scan results and the test_report you attached, and sign with their own key; "
            "nothing you can call approves it. Check back with get_deployment_status."
        )
        result = pending_approval(data)
    else:
        result = success(data)

    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result


# Section 13.7 anticipates "secret references" in this tool's output (its
# security note keeps them from auditors), and SEC-SECRET-3 lets the agent
# know *which* secrets exist — never their values. Relayed through an
# explicit allowlist of fields, so even if the Platform API ever returned
# more, a value could not ride along.
_SECRET_REFERENCE_FIELDS = ("name", "managed_by", "version", "updated_at")


async def _secret_references(
    client: PlatformClient, application_id: str
) -> tuple[list[dict[str, Any]] | None, str | None]:
    try:
        rows = await client.list_secrets(application_id)
    except ToolError as exc:
        # Owner-only on the Platform API. For anyone else the honest answer
        # is "not visible to you" — never [], which would claim there are
        # none (the same mistake the Admin Portal's first draft made).
        if exc.code != ErrorCode.UNAUTHORIZED:
            raise
        return None, "Only this application's owners can see its secret names."
    return [{field: row.get(field) for field in _SECRET_REFERENCE_FIELDS} for row in rows], None


async def get_application_status(client: PlatformClient, application_id: str) -> dict[str, Any]:
    app = await client.get_application(application_id)
    latest_deployment_id = None
    urls: dict[str, str] = {}
    try:
        latest = await client.latest_deployment(application_id)
        latest_deployment_id = latest.get("id")
        if latest.get("status") == "running":
            urls = _service_urls(latest)
    except ToolError as exc:
        if exc.code != ErrorCode.NOT_FOUND:
            raise

    secrets, secrets_note = await _secret_references(client, application_id)
    active_alerts = None
    try:
        alerts = await client.get_alerts(application_id)
        active_alerts = [
            {k: a.get(k) for k in ("service", "rule", "level", "message", "started_at")} for a in alerts.get("active", [])
        ]
    except ToolError:
        pass  # only owners see alerts, same as secrets

    return success(
        {
            "application_id": application_id,
            "name": app.get("name"),
            "current_lifecycle_state": app.get("lifecycle_status"),
            "latest_deployment_id": latest_deployment_id,
            "url": _primary_url(urls),
            "urls": urls,
            "secrets": secrets,
            "secrets_note": secrets_note,
            # What the platform's alert rules (FR-092) say is wrong right
            # now; None when this caller can't see them.
            "active_alerts": active_alerts,
        }
    )


async def get_deployment_status(client: PlatformClient, deployment_id: str) -> dict[str, Any]:
    deployment = await client.get_deployment(deployment_id)
    phase_map = {
        "scanning": "IMAGE_SCAN",
        "pending_approval": "PENDING_APPROVAL",
        "deploying": "DEPLOYING",
        "health_check": "HEALTH_CHECK",
        "running": "COMPLETED",
        "failed": "FAILED",
        "rejected": "FAILED",
        "superseded": "COMPLETED",
        "suspended": "COMPLETED",
        "archived": "COMPLETED",
    }
    phase = phase_map.get(deployment.get("status", ""), "UNKNOWN")

    result_summary: dict[str, Any] = {}
    if phase == "COMPLETED" and deployment.get("status") == "running":
        urls = _service_urls(deployment)
        result_summary["url"] = _primary_url(urls)
        result_summary["urls"] = urls
    if phase == "FAILED":
        result_summary["reason"] = deployment.get("failure_reason") or deployment.get("rejection_reason")

    return success(
        {
            "deployment_id": deployment_id,
            "application_id": deployment.get("application_id"),
            "phase": phase,
            "started_at": deployment.get("created_at"),
            "updated_at": deployment.get("updated_at"),
            "result": result_summary,
        }
    )


async def rollback_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    target_version: str,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    fingerprint = (application_id, target_version)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    target_deployment_id = target_version
    if target_version.strip().lower() == "previous":
        history = await client.deployment_history(application_id)
        # history is newest-first (see platform-api's ListForApplication);
        # entry 0 is the current one, so the most recent PRIOR successful
        # deployment is the first "superseded"/"running" entry after it.
        candidates = [
            d for d in history[1:] if d.get("status") in ("running", "superseded")
        ]
        if not candidates:
            raise ToolError(
                ErrorCode.NOT_FOUND,
                'target_version="previous" requested, but this application has no '
                "prior successful deployment to roll back to",
            )
        target_deployment_id = candidates[0]["id"]

    deployment = await client.rollback_application(application_id, target_deployment_id)
    result = success(
        {
            "deployment_id": deployment.get("id"),
            "application_id": application_id,
            "status": deployment.get("status"),
            "target_version": target_deployment_id,
        }
    )
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result


async def restart_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    fingerprint = (application_id,)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    deployment = await client.restart_application(application_id)
    result = success(
        {
            "status": "COMPLETED",
            "application_id": application_id,
            "restarted_at": deployment.get("updated_at"),
        }
    )
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result
