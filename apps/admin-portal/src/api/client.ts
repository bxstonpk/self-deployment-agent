import type { Identity } from "../identity";
import { identityHeaders } from "../identity";
import {
  ApiError,
  type Application,
  type ApplicationOwner,
  type AuditEntry,
  type AuditQueryParams,
  type Build,
  type Department,
  type DepartmentRaw,
  type Deployment,
  type Notification,
  type OwnershipRole,
  type OwnershipTransfer,
  type ScaleEvent,
  type SupportedStack,
  type SupportedStackRaw,
} from "./types";

// Same base URL for the whole session — configured once via Vite env, not
// re-derived per call. See .env.example in this app's own directory.
const BASE_URL = import.meta.env.VITE_PLATFORM_API_BASE_URL ?? "http://localhost:8090";

async function request<T>(
  identity: Identity,
  method: string,
  path: string,
  options: { json?: unknown; body?: BodyInit; contentType?: string } = {},
): Promise<T> {
  const headers: Record<string, string> = { ...identityHeaders(identity) };
  let body: BodyInit | undefined;

  if (options.json !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(options.json);
  } else if (options.body !== undefined) {
    if (options.contentType) headers["Content-Type"] = options.contentType;
    body = options.body;
  }

  let res: Response;
  try {
    res = await fetch(`${BASE_URL}${path}`, { method, headers, body });
  } catch (err) {
    throw new ApiError(0, "NETWORK_ERROR", `could not reach the Platform API: ${String(err)}`);
  }

  if (!res.ok) {
    let code = "internal_error";
    let message = `Platform API returned HTTP ${res.status}`;
    try {
      const parsed = (await res.json()) as { error?: { code?: string; message?: string } };
      if (parsed.error) {
        code = parsed.error.code ?? code;
        message = parsed.error.message ?? message;
      }
    } catch {
      // body wasn't JSON — keep the generic message
    }
    throw new ApiError(res.status, code, message);
  }

  if (res.status === 204 || res.headers.get("content-length") === "0") {
    return undefined as T;
  }
  return (await res.json()) as T;
}

// --- Applications -----------------------------------------------------

export function listApplications(identity: Identity): Promise<{ applications: Application[] }> {
  return request(identity, "GET", "/applications");
}

export function getApplication(identity: Identity, id: string): Promise<Application> {
  return request(identity, "GET", `/applications/${id}`);
}

export function registerApplication(
  identity: Identity,
  input: { name: string; description: string; owning_department_id: string },
): Promise<Application> {
  return request(identity, "POST", "/applications", { json: input });
}

export function saveDeploymentYaml(identity: Identity, id: string, deploymentYaml: string): Promise<Application> {
  return request(identity, "PUT", `/applications/${id}/deployment-yaml`, {
    json: { deployment_yaml: deploymentYaml },
  });
}

export function validateApplication(
  identity: Identity,
  id: string,
): Promise<{ application: Application; report: import("./types").ValidationReport }> {
  return request(identity, "POST", `/applications/${id}/validate`);
}

// --- Build --------------------------------------------------------------

// Raw bytes upload — the source archive IS the request body, matching
// services/platform-api's own convention (see its README's "How Build
// works"). A browser file picker is a genuinely better fit for this than
// the MCP path's base64-JSON workaround (services/mcp-server), since a
// human operating this UI already has the file, not a base64 string.
export function triggerBuild(identity: Identity, id: string, archive: File | Blob): Promise<Build> {
  return request(identity, "POST", `/applications/${id}/build`, {
    body: archive,
    contentType: "application/gzip",
  });
}

export function latestBuild(identity: Identity, id: string): Promise<Build> {
  return request(identity, "GET", `/applications/${id}/builds/latest`);
}

// --- Deployment -----------------------------------------------------------

export function deployApplication(
  identity: Identity,
  id: string,
  environment: "dev" | "production",
): Promise<Deployment> {
  return request(identity, "POST", `/applications/${id}/deploy`, { json: { environment } });
}

export function latestDeployment(identity: Identity, id: string): Promise<Deployment> {
  return request(identity, "GET", `/applications/${id}/deployments/latest`);
}

export function deploymentHistory(identity: Identity, id: string): Promise<Deployment[]> {
  return request(identity, "GET", `/applications/${id}/deployments`);
}

export function rollbackApplication(
  identity: Identity,
  id: string,
  targetDeploymentId: string,
): Promise<Deployment> {
  return request(identity, "POST", `/applications/${id}/rollback`, {
    json: { target_deployment_id: targetDeploymentId },
  });
}

export function restartApplication(identity: Identity, id: string): Promise<Deployment> {
  return request(identity, "POST", `/applications/${id}/restart`);
}

// --- Lifecycle: Suspend/Resume/Archive/Delete ------------------------------

export function suspendApplication(identity: Identity, id: string): Promise<Deployment> {
  return request(identity, "POST", `/applications/${id}/suspend`);
}

export function resumeApplication(identity: Identity, id: string): Promise<Deployment> {
  return request(identity, "POST", `/applications/${id}/resume`);
}

export function archiveApplication(identity: Identity, id: string): Promise<Application> {
  return request(identity, "POST", `/applications/${id}/archive`);
}

export function deleteApplication(identity: Identity, id: string): Promise<Application> {
  return request(identity, "POST", `/applications/${id}/delete`, { json: { confirm: true } });
}

// --- Scale events -----------------------------------------------------

export function listScaleEvents(identity: Identity, id: string): Promise<{ scale_events: ScaleEvent[] }> {
  return request(identity, "GET", `/applications/${id}/scale-events`);
}

// --- Ownership (Module E) --------------------------------------------------

export function listOwners(identity: Identity, id: string): Promise<{ owners: ApplicationOwner[] }> {
  return request(identity, "GET", `/applications/${id}/owners`);
}

// Primary-owner-only server-side (platform-api/README.md's "How
// Co-Owner/Contributor Management works") — this app doesn't gate the
// button on that client-side (no way to resolve "is the signed-in
// identity the primary owner" without an extra lookup this app doesn't
// have), so a non-primary owner attempting this sees the real 403 via the
// error banner, same as every other owner-gated action here.
export function grantOwner(identity: Identity, id: string, email: string, ownershipRole: OwnershipRole): Promise<ApplicationOwner> {
  return request(identity, "POST", `/applications/${id}/owners`, {
    json: { email, ownership_role: ownershipRole },
  });
}

export function revokeOwner(identity: Identity, id: string, userId: string): Promise<void> {
  return request(identity, "DELETE", `/applications/${id}/owners/${userId}`);
}

// Primary-owner-only server-side, same "don't hide, let the real error
// surface" convention as grantOwner above.
export function initiateOwnershipTransfer(identity: Identity, id: string, email: string): Promise<OwnershipTransfer> {
  return request(identity, "POST", `/applications/${id}/ownership-transfer`, { json: { email } });
}

// "No transfer currently pending" is the ordinary state of most
// applications most of the time, not an error — the server responds
// `{"transfer": null}` with a real 200 for it, not a 404 (platform-api's
// GetPendingTransfer doc comment covers why: an earlier version of this
// endpoint DID 404, and real browser testing caught that as console noise
// on every single application detail page view, not just an occasional
// mistaken lookup).
export async function getPendingOwnershipTransfer(identity: Identity, id: string): Promise<OwnershipTransfer | null> {
  const data = await request<{ transfer: OwnershipTransfer | null }>(identity, "GET", `/applications/${id}/ownership-transfer`);
  return data.transfer;
}

// Only the nominated user can succeed here server-side — this app doesn't
// hide the button from anyone else viewing the page (no way to resolve
// "am I the nominee" client-side without an extra lookup), same
// convention as every other owner-gated action here.
export function acceptOwnershipTransfer(identity: Identity, transferId: string): Promise<ApplicationOwner> {
  return request(identity, "POST", `/ownership-transfers/${transferId}/accept`);
}

// --- Departments / Supported Stacks (raw PascalCase — see types.ts) -------

export async function listDepartments(identity: Identity): Promise<Department[]> {
  const data = await request<{ departments: DepartmentRaw[] }>(identity, "GET", "/departments");
  return data.departments.map((d) => ({
    id: d.ID,
    name: d.Name,
    cost_center_code: d.CostCenterCode,
    status: d.Status,
  }));
}

export async function listSupportedStacks(identity: Identity): Promise<SupportedStack[]> {
  const data = await request<{ stacks: SupportedStackRaw[] }>(identity, "GET", "/supported-stacks");
  return data.stacks.map((s) => ({ id: s.ID, kind: s.Kind, name: s.Name, status: s.Status }));
}

// --- Audit Log (Module W) -------------------------------------------------

function auditQueryString(params: AuditQueryParams = {}): string {
  const q = new URLSearchParams();
  if (params.actorUserId) q.set("actor_user_id", params.actorUserId);
  if (params.resourceType) q.set("resource_type", params.resourceType);
  if (params.resourceId) q.set("resource_id", params.resourceId);
  if (params.action) q.set("action", params.action);
  if (params.from) q.set("from", params.from);
  if (params.to) q.set("to", params.to);
  if (params.limit) q.set("limit", String(params.limit));
  const s = q.toString();
  return s ? `?${s}` : "";
}

// GET /audit-log is scoped server-side (AuditService.Query) to entries the
// caller performed themselves or that concern an application they own —
// there is no broader Auditor/Security Administrator role to request more
// than that with yet (see services/platform-api/README.md's "How Audit
// Logging works" section).
export function queryAuditLog(identity: Identity, params?: AuditQueryParams): Promise<{ entries: AuditEntry[] }> {
  return request(identity, "GET", `/audit-log${auditQueryString(params)}`);
}

// Not routed through request<T>() like everything else here: the response
// is a CSV file, not JSON, and needs the identity headers a plain <a href>
// download link can't carry.
export async function exportAuditLogCsv(identity: Identity, params?: AuditQueryParams): Promise<Blob> {
  const res = await fetch(`${BASE_URL}/audit-log/export${auditQueryString(params)}`, {
    headers: identityHeaders(identity),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "export_failed", `audit log export failed with HTTP ${res.status}`);
  }
  return res.blob();
}

export function verifyAuditLogIntegrity(identity: Identity): Promise<{ intact: boolean; broken_at_seq?: number }> {
  return request(identity, "GET", "/audit-log/integrity");
}

// --- Notifications (Module X) ---------------------------------------------

// Always the caller's own inbox — GET /notifications has no "whose"
// parameter, it's scoped server-side to the authenticated caller.
export function listNotifications(identity: Identity, unreadOnly = false): Promise<{ notifications: Notification[] }> {
  return request(identity, "GET", `/notifications${unreadOnly ? "?unread_only=true" : ""}`);
}

export function markNotificationRead(identity: Identity, id: string): Promise<Notification> {
  return request(identity, "POST", `/notifications/${id}/read`);
}
