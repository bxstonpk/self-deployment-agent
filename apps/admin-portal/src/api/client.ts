import type { Identity } from "../identity";
import { identityHeaders } from "../identity";
import {
  ApiError,
  type Application,
  type Build,
  type Department,
  type DepartmentRaw,
  type Deployment,
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
