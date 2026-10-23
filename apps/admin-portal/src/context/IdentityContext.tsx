import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import type { Identity } from "../identity";
import { clearIdentity, loadIdentity, saveIdentity, UNAUTHORIZED_EVENT } from "../identity";
import { logout } from "../api/client";

interface IdentityContextValue {
  identity: Identity | null;
  signIn: (identity: Identity) => void;
  signOut: () => void;
}

const IdentityContext = createContext<IdentityContextValue | undefined>(undefined);

export function IdentityProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<Identity | null>(() => loadIdentity());

  // The Platform API rejected the session — expired, revoked, or signed
  // out in another tab. Forget it so RequireIdentity sends the user to sign
  // in rather than leaving every page failing.
  useEffect(() => {
    const onUnauthorized = () => {
      clearIdentity();
      setIdentity(null);
    };
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, []);

  const signIn = (next: Identity) => {
    saveIdentity(next);
    setIdentity(next);
  };
  const signOut = () => {
    // Revoke the session on the platform too, so a copied token stops
    // working; signing out locally doesn't wait for it.
    if (identity) logout(identity).catch(() => {});
    clearIdentity();
    setIdentity(null);
  };

  return (
    <IdentityContext.Provider value={{ identity, signIn, signOut }}>{children}</IdentityContext.Provider>
  );
}

export function useIdentity(): IdentityContextValue {
  const ctx = useContext(IdentityContext);
  if (!ctx) throw new Error("useIdentity must be used within an IdentityProvider");
  return ctx;
}
