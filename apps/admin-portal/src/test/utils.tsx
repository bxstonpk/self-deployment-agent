import type { ReactElement } from "react";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { IdentityProvider } from "../context/IdentityContext";

// Pre-seeds a signed-in dev identity in localStorage so pages behind
// RequireIdentity render immediately, matching how a returning user (one
// who already signed in this browser) experiences the app.
export function signInAs(email = "alice@example.com") {
  localStorage.setItem("admin-portal.identity", JSON.stringify({ email }));
}

export function renderWithProviders(ui: ReactElement, { route = "/" }: { route?: string } = {}) {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <IdentityProvider>{ui}</IdentityProvider>
    </MemoryRouter>,
  );
}
