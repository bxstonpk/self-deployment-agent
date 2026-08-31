import { createContext, useContext, useState, type ReactNode } from "react";
import type { Identity } from "../identity";
import { clearIdentity, loadIdentity, saveIdentity } from "../identity";

interface IdentityContextValue {
  identity: Identity | null;
  signIn: (identity: Identity) => void;
  signOut: () => void;
}

const IdentityContext = createContext<IdentityContextValue | undefined>(undefined);

export function IdentityProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<Identity | null>(() => loadIdentity());

  const signIn = (next: Identity) => {
    saveIdentity(next);
    setIdentity(next);
  };
  const signOut = () => {
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
