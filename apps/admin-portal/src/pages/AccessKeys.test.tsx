import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AccessKeys } from "./AccessKeys";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const KEY = {
  id: "k1",
  kind: "api_key",
  name: "DESKTOP-42 (alice, Claude Code)",
  prefix: "cpk_AbCdEfGh",
  created_at: "2026-10-23T09:00:00Z",
  expires_at: "2026-11-22T09:00:00Z",
  last_used_at: null,
  revoked_at: null,
  active: true,
};
const REVOKED = { ...KEY, revoked_at: "2026-10-23T10:00:00Z", active: false };

describe("AccessKeys", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs("alice@example.com");
  });
  afterEach(() => vi.unstubAllGlobals());

  it("lists keys by prefix only and revokes one", async () => {
    let revoked = false;
    const fetchMock = vi.fn().mockImplementation((_url: string, options?: RequestInit) => {
      if (options?.method === "DELETE") {
        revoked = true;
        return Promise.resolve(jsonResponse({ credential: REVOKED }));
      }
      return Promise.resolve(jsonResponse({ api_keys: [revoked ? REVOKED : KEY] }));
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("confirm", () => true);
    const user = userEvent.setup();
    renderWithProviders(<AccessKeys />, { route: "/access-keys" });

    const row = (await screen.findByText(/DESKTOP-42/)).closest("tr")!;
    expect(within(row).getByText("cpk_AbCdEfGh…")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: /revoke/i }));

    await waitFor(() => expect(screen.getByText("revoked")).toBeInTheDocument());
    expect(fetchMock.mock.calls.some((c) => (c[0] as string).endsWith("/auth/api-keys/k1"))).toBe(true);
  });

  it("shows a hand-issued key's value once, then never again", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, options?: RequestInit) => {
      if (options?.method === "POST") {
        return Promise.resolve(
          jsonResponse({ api_key: "cpk_the-only-time-you-see-this", credential: { ...KEY, id: "k2", name: "build-01" } }, 201),
        );
      }
      return Promise.resolve(jsonResponse({ api_keys: [] }));
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<AccessKeys />, { route: "/access-keys" });

    await screen.findByText(/no keys yet/i);
    await user.type(screen.getByLabelText(/^name/i), "build-01");
    await user.click(screen.getByRole("button", { name: /issue key/i }));

    expect(await screen.findByTestId("issued-key")).toHaveTextContent("cpk_the-only-time-you-see-this");
    await user.click(screen.getByRole("button", { name: /stored it/i }));
    expect(screen.queryByText("cpk_the-only-time-you-see-this")).toBeNull();
  });
});
