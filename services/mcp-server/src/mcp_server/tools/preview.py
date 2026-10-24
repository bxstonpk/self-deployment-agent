"""Previews (DEC-029 part 3): try a change on the platform before deploying it.

    preview_application  test, build and run the project as a short-lived copy of the application
    stop_preview         tear it down now

A preview is its own application, "<name>-preview-<4 chars>", with its own
empty database and its own address; the application it previews keeps
serving, untouched. It is deleted when it expires (24 hours by default), or
when its application is archived or deleted. Starting another replaces it.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

from ..envelope import ErrorCode, ToolError, success
from ..platform_client import PlatformClient
from ..source_packer import PackError, pack_project


async def preview_application(
    client: PlatformClient, application_id: str, project_path: str, run_tests: bool = True,
) -> dict[str, Any]:
    app = await client.get_application(application_id)
    try:
        parsed = yaml.safe_load(app.get("deployment_yaml_draft") or "") or {}
    except yaml.YAMLError as exc:
        raise ToolError(ErrorCode.VALIDATION_ERROR, f"the saved deployment.yaml doesn't parse: {exc}") from exc
    services = [str(s) for s in (parsed.get("services") or {})] if isinstance(parsed, dict) else []
    try:
        archive, summary = pack_project(str(Path(project_path).expanduser()), services)
    except PackError as exc:
        raise ToolError(ErrorCode.VALIDATION_ERROR, str(exc)) from exc

    tests: dict[str, Any] | None = None
    if run_tests:
        tests = await client.run_tests(application_id, archive)
        if tests.get("status") != "passed":
            report = (tests.get("report") or "").rstrip("\n").split("\n")
            raise ToolError(
                ErrorCode.VALIDATION_ERROR,
                f"the tests didn't pass on the platform ({tests.get('status')}); no preview was made.\n"
                + "\n".join(report[-60:]),
            )

    started = await client.start_preview(application_id, archive)
    preview = started.get("preview", {})
    build = started.get("build", {})
    deployment = started.get("deployment") or {}
    if build.get("status") == "failed":
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "the preview didn't build (it was removed): " + (build.get("error_detail") or "no detail").removeprefix("build failed: "),
        )
    data: dict[str, Any] = {
        "preview": preview,
        "url": preview.get("url"),
        "expires_at": preview.get("expires_at"),
        "deployment_status": deployment.get("status"),
        "packed": summary.as_dict(),
        "note": (
            "A separate, short-lived copy with its own EMPTY database: the application it previews "
            "and its data are untouched. Share the url to try it. It is deleted at expires_at; "
            "preview_application again replaces it, stop_preview removes it now."
        ),
    }
    if tests is not None:
        data["test_run_id"] = tests.get("id")
    if deployment.get("status") not in (None, "running"):
        data["deployment_note"] = (
            f"the preview's deployment is {deployment.get('status')}: {deployment.get('failure_reason') or ''} "
            "Read its logs with get_application_logs on the preview's application_id."
        )
    return success(data)


async def stop_preview(client: PlatformClient, application_id: str) -> dict[str, Any]:
    return success({"application_id": application_id, **(await client.stop_preview(application_id))})
