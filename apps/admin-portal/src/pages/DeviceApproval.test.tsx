import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DeviceApproval } from "./DeviceApproval";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const DEVICE = {
  user_code: "BCDF-GHJK",
  client_name: "DESKTOP-42 (alice, Claude Code)",
  status: "pending",
  created_at: "2026-10-23T09:00:00Z",
  expires_at: "2026-10-23T09:10:00Z",
};

describe("DeviceApproval", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs("alice@example.com");
  });
  afterEach(() => vi.unstubAllGlobals());

  it("looks up the code from the link, shows which machine is asking, and approves it", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, options?: RequestInit) =>
      Promise.resolve(jsonResponse({ device: { ...DEVICE, status: options?.method === "POST" ? "approved" : "pending" } })),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<DeviceApproval />, { route: "/device?code=BCDF-GHJK" });

    expect(await screen.findByText(/DESKTOP-42/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /approve/i }));

    expect(await screen.findByRole("status")).toHaveTextContent(/approved/i);
    const approveCall = fetchMock.mock.calls.find((c) => (c[0] as string).endsWith("/auth/device/BCDF-GHJK/approve"));
    expect(approveCall).toBeDefined();
    expect(((approveCall![1] as RequestInit).headers as Record<string, string>).Authorization).toBe(
      "Bearer cps_test-session-alice@example.com",
    );
  });

  it("explains an unknown or expired code instead of offering to approve it", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: { code: "user_code_not_found", message: "no" } }, 404)),
    );
    renderWithProviders(<DeviceApproval />, { route: "/device?code=ZZZZ-ZZZZ" });

    expect(await screen.findByRole("alert")).toHaveTextContent(/expired/i);
    expect(screen.queryByRole("button", { name: /approve/i })).toBeNull();
  });

  it("denies a request", async () => {
    // A fresh Response per call: a body can only be read once.
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ device: DEVICE })));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<DeviceApproval />, { route: "/device?code=BCDF-GHJK" });

    await user.click(await screen.findByRole("button", { name: /deny/i }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some((c) => (c[0] as string).endsWith("/auth/device/BCDF-GHJK/deny"))).toBe(true),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(/denied/i);
  });
});
