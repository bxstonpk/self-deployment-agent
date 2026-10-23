// The signed-in employee, per DEC-001/DEC-003 (revised 2026-10-23,
// docs/17_Decision_Log.md): an email and password exchanged for a session
// token, which every Platform API call carries as a Bearer credential. The
// Platform API re-derives every real check from that token server-side;
// nothing else here is trusted.
//
// Kept in localStorage so a reload stays signed in for the session's
// lifetime (12h by default). An identity saved by the old self-declared
// sign-in has no token and is ignored, which signs that browser out.

export interface Identity {
  email: string;
  name?: string;
  token: string;
  expiresAt?: string;
}

const STORAGE_KEY = "admin-portal.identity";

export function loadIdentity(): Identity | null {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Identity;
    if (!parsed.email || !parsed.token) return null;
    if (parsed.expiresAt && Date.parse(parsed.expiresAt) <= Date.now()) return null;
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
  return { Authorization: `Bearer ${identity.token}` };
}

// Fired when the Platform API rejects the session (expired, revoked, or
// signed out elsewhere); IdentityProvider listens and signs the tab out.
export const UNAUTHORIZED_EVENT = "admin-portal:unauthorized";
