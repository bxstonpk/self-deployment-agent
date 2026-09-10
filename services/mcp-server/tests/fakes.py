"""In-memory fake standing in for PlatformClient in unit tests — mirrors
services/platform-api's own fakeXRepo pattern (in-memory maps, no network),
just on the Python/MCP side of the boundary.
"""

from __future__ import annotations

import uuid
from typing import Any

from mcp_server.envelope import ErrorCode, ToolError


class FakePlatformClient:
    def __init__(self) -> None:
        self.departments: list[dict[str, Any]] = [
            {"id": "dept-eng", "name": "Engineering", "cost_center_code": "ENG-01", "status": "active"},
        ]
        self.applications: dict[str, dict[str, Any]] = {}
        self.deployments: dict[str, dict[str, Any]] = {}
        self.stacks: list[dict[str, Any]] = [
            {"id": "stack-go", "category": "backend", "runtime": "go", "status": "active"},
            {"id": "stack-react", "category": "frontend", "runtime": "react", "status": "active"},
        ]
        self.calls: list[tuple[str, tuple[Any, ...]]] = []
        self._clock = 0
        # Overridable per-test: what trigger_build returns next. Defaults
        # to a successful build.
        self.next_build_result: dict[str, Any] = {"status": "succeeded", "image_refs": {"api": "platform-build/x:1"}}
        self.audit_entries: list[dict[str, Any]] = []
        self.notifications: list[dict[str, Any]] = []
        self.inventory: list[dict[str, Any]] = []
        self.activity: dict[str, Any] = {
            "from": "2026-01-01T00:00:00Z",
            "to": "2026-01-31T00:00:00Z",
            "total": {"succeeded": 0, "failed": 0, "rolled_back": 0},
            "by_environment": {},
            "by_department": {},
        }
        self.owners: dict[str, list[dict[str, Any]]] = {}

    def _record(self, name: str, *args: Any) -> None:
        self.calls.append((name, args))

    def _tick(self) -> str:
        # Monotonically increasing timestamp — deployment_history's
        # newest-first ordering depends on distinct created_at values, and
        # a fixed literal (as most other fakes use) would tie every
        # deployment created within one test, same class of flakiness
        # fixed on the Go side in fakeDeploymentRepo.Create.
        self._clock += 1
        return f"2026-01-01T00:{self._clock:02d}:00Z"

    async def list_departments(self) -> list[dict[str, Any]]:
        self._record("list_departments")
        return self.departments

    async def register_application(self, name, description, owning_department_id) -> dict[str, Any]:
        self._record("register_application", name, description, owning_department_id)
        for app in self.applications.values():
            if app["name"] == name:
                raise ToolError(ErrorCode.CONFLICT, "application name already registered")
        app_id = f"app-{uuid.uuid4().hex[:8]}"
        app = {
            "id": app_id,
            "name": name,
            "description": description,
            "owning_department_id": owning_department_id,
            "lifecycle_status": "draft",
            "deployment_yaml_draft": None,
            "created_at": "2026-01-01T00:00:00Z",
        }
        self.applications[app_id] = app
        return dict(app)

    async def get_application(self, application_id: str) -> dict[str, Any]:
        self._record("get_application", application_id)
        app = self.applications.get(application_id)
        if not app:
            raise ToolError(ErrorCode.NOT_FOUND, "application not found")
        return dict(app)

    async def trigger_build(self, application_id: str, source_archive: bytes) -> dict[str, Any]:
        self._record("trigger_build", application_id, source_archive)
        return dict(self.next_build_result)

    async def save_deployment_yaml(self, application_id: str, deployment_yaml: str) -> dict[str, Any]:
        self._record("save_deployment_yaml", application_id, deployment_yaml)
        app = self.applications[application_id]
        app["deployment_yaml_draft"] = deployment_yaml
        app["lifecycle_status"] = "draft"
        return dict(app)

    async def validate_application(self, application_id: str) -> dict[str, Any]:
        self._record("validate_application", application_id)
        app = self.applications[application_id]
        app["lifecycle_status"] = "validated"
        return {
            "application": dict(app),
            "report": {"valid": True, "checks": [{"name": "schema", "status": "passed"}]},
        }

    async def get_supported_stacks(self) -> list[dict[str, Any]]:
        self._record("get_supported_stacks")
        return self.stacks

    async def deploy_application(self, application_id: str, environment: str) -> dict[str, Any]:
        self._record("deploy_application", application_id, environment)
        app = self.applications[application_id]
        dep_id = f"dep-{uuid.uuid4().hex[:8]}"
        status = "pending_approval" if environment == "production" else "running"
        timestamp = self._tick()
        deployment = {
            "id": dep_id,
            "application_id": application_id,
            "status": status,
            "environment": environment,
            "containers": {"api": {"url": "http://localhost:9999"}} if status == "running" else {},
            "created_at": timestamp,
            "updated_at": timestamp,
        }
        self.deployments[dep_id] = deployment
        app["lifecycle_status"] = "running" if status == "running" else app["lifecycle_status"]
        app["_latest_deployment_id"] = dep_id
        return dict(deployment)

    async def latest_deployment(self, application_id: str) -> dict[str, Any]:
        self._record("latest_deployment", application_id)
        app = self.applications.get(application_id) or {}
        dep_id = app.get("_latest_deployment_id")
        if not dep_id:
            raise ToolError(ErrorCode.NOT_FOUND, "no deployment yet")
        return dict(self.deployments[dep_id])

    async def deployment_history(self, application_id: str) -> list[dict[str, Any]]:
        self._record("deployment_history", application_id)
        history = [d for d in self.deployments.values() if d["application_id"] == application_id]
        return sorted(history, key=lambda d: d["created_at"], reverse=True)

    async def get_deployment(self, deployment_id: str) -> dict[str, Any]:
        self._record("get_deployment", deployment_id)
        dep = self.deployments.get(deployment_id)
        if not dep:
            raise ToolError(ErrorCode.NOT_FOUND, "deployment not found")
        return dict(dep)

    async def rollback_application(self, application_id: str, target_deployment_id: str) -> dict[str, Any]:
        self._record("rollback_application", application_id, target_deployment_id)
        target = self.deployments.get(target_deployment_id)
        if not target or target["status"] not in ("running", "superseded"):
            raise ToolError(
                ErrorCode.CONFLICT, "invalid rollback target", platform_code="invalid_rollback_target"
            )
        for d in self.deployments.values():
            if d["application_id"] == application_id and d["status"] == "running":
                d["status"] = "superseded"
        new_id = f"dep-{uuid.uuid4().hex[:8]}"
        timestamp = self._tick()
        deployment = {**target, "id": new_id, "status": "running", "created_at": timestamp, "updated_at": timestamp}
        self.deployments[new_id] = deployment
        self.applications[application_id]["_latest_deployment_id"] = new_id
        return dict(deployment)

    async def restart_application(self, application_id: str) -> dict[str, Any]:
        self._record("restart_application", application_id)
        dep = await self.latest_deployment(application_id)
        dep["updated_at"] = "2026-01-01T01:00:00Z"
        return dep

    async def archive_application(self, application_id: str) -> dict[str, Any]:
        self._record("archive_application", application_id)
        app = self.applications[application_id]
        app["lifecycle_status"] = "archived"
        return dict(app)

    async def delete_application(self, application_id: str) -> dict[str, Any]:
        self._record("delete_application", application_id)
        app = self.applications[application_id]
        app["lifecycle_status"] = "deleted"
        return dict(app)

    async def query_audit_log(self, params: dict[str, Any]) -> list[dict[str, Any]]:
        self._record("query_audit_log", params)
        entries = self.audit_entries
        if params.get("resource_type"):
            entries = [e for e in entries if e["resource_type"] == params["resource_type"]]
        if params.get("resource_id"):
            entries = [e for e in entries if e["resource_id"] == params["resource_id"]]
        if params.get("action"):
            entries = [e for e in entries if e["action"] == params["action"]]
        return list(entries)

    async def list_notifications(self, unread_only: bool) -> list[dict[str, Any]]:
        self._record("list_notifications", unread_only)
        if unread_only:
            return [n for n in self.notifications if n.get("read_at") is None]
        return list(self.notifications)

    async def application_inventory(self) -> list[dict[str, Any]]:
        self._record("application_inventory")
        return list(self.inventory)

    async def deployment_activity(self, from_time: str | None, to_time: str | None) -> dict[str, Any]:
        self._record("deployment_activity", from_time, to_time)
        return dict(self.activity)

    async def list_owners(self, application_id: str) -> list[dict[str, Any]]:
        self._record("list_owners", application_id)
        return list(self.owners.get(application_id, []))

    async def grant_owner(self, application_id: str, email: str, ownership_role: str) -> dict[str, Any]:
        self._record("grant_owner", application_id, email, ownership_role)
        owner = {"user_id": f"user-{email}", "ownership_role": ownership_role, "status": "active"}
        self.owners.setdefault(application_id, []).append(owner)
        return owner

    async def revoke_owner(self, application_id: str, user_id: str) -> dict[str, Any]:
        self._record("revoke_owner", application_id, user_id)
        remaining = [o for o in self.owners.get(application_id, []) if o["user_id"] != user_id]
        if len(remaining) == len(self.owners.get(application_id, [])):
            raise ToolError(ErrorCode.NOT_FOUND, "no active co-owner/contributor grant found")
        self.owners[application_id] = remaining
        return {}

    async def mark_notification_read(self, notification_id: str) -> dict[str, Any]:
        self._record("mark_notification_read", notification_id)
        for n in self.notifications:
            if n["id"] == notification_id:
                n["read_at"] = "2026-01-01T01:00:00Z"
                return dict(n)
        raise ToolError(ErrorCode.NOT_FOUND, "notification not found")
