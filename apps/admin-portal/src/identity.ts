// Dev-mode identity, mirroring services/platform-api's own DevHeaderAuthenticator
// (internal/httpapi/devauth.go) — the same DEC-001-blocked stand-in used
// everywhere else in this project (the MCP server binds one identity per
// process; this binds one identity per browser tab via localStorage). Not a
// security boundary — the Platform API re-derives every real check
// server-side regardless of what's sent here.

export interface Identity {
  email: string;
  name?: string;
  department?: string;
}

const STORAGE_KEY = "admin-portal.identity";

export function loadIdentity(): Identity | null {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Identity;
    if (!parsed.email) return null;
    return parsed;
  } catch {
    return null;
  }
}

export function saveIdentity(identity: Identity): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(identity));
}

export function clearIdentity(): void {
  localStorage.removeItem(STORAGE_KEY);
}

export function identityHeaders(identity: Identity): Record<string, string> {
  const headers: Record<string, string> = { "X-Dev-User-Email": identity.email };
  if (identity.name) headers["X-Dev-User-Name"] = identity.name;
  if (identity.department) headers["X-Dev-Department"] = identity.department;
  return headers;
}
