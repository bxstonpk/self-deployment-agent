import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditLog } from "./AuditLog";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const ENTRIES = [
  {
    id: "audit-1", seq: 2, occurred_at: "2026-01-02T00:00:00Z", actor_user_id: "user-1",
    action: "application.suspend", resource_type: "application", resource_id: "app-1",
    outcome: "success", detail: "", entry_hash: "abc",
  },
  {
    id: "audit-2", seq: 1, occurred_at: "2026-01-01T00:00:00Z", actor_user_id: "user-1",
    action: "application.register", resource_type: "application", resource_id: "app-1",
    outcome: "success", detail: "", entry_hash: "def",
  },
];

describe("AuditLog", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders audit entries returned by the API", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ entries: ENTRIES })));

    renderWithProviders(<AuditLog />, { route: "/audit-log" });

    expect(await screen.findByText("application.suspend")).toBeInTheDocument();
    expect(screen.getByText("application.register")).toBeInTheDocument();
  });

  it("re-queries with the action filter applied", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ entries: ENTRIES }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<AuditLog />, { route: "/audit-log" });
    await screen.findByText("application.suspend");

    await user.type(screen.getByLabelText(/filter by action/i), "application.suspend");
    await user.click(screen.getByRole("button", { name: /apply filters/i }));

    await waitFor(() => {
      const lastCall = fetchMock.mock.calls.at(-1) as [string, RequestInit];
      expect(lastCall[0]).toContain("action=application.suspend");
    });
  });

  it("surfaces an API error instead of hanging on a blank loading state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: { code: "INTERNAL_ERROR", message: "database unreachable" } }, 500)),
    );

    renderWithProviders(<AuditLog />, { route: "/audit-log" });

    expect(await screen.findByText(/database unreachable/i)).toBeInTheDocument();
  });

  it("reports chain integrity from GET /audit-log/integrity", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/integrity")) return Promise.resolve(jsonResponse({ intact: true }));
      return Promise.resolve(jsonResponse({ entries: [] }));
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<AuditLog />, { route: "/audit-log" });
    await screen.findByText(/no audit entries match/i);

    await user.click(screen.getByRole("button", { name: /verify chain integrity/i }));

    expect(await screen.findByText(/chain intact/i)).toBeInTheDocument();
  });

  it("downloads a CSV export via the export endpoint with identity headers", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes("/export")) {
        return Promise.resolve(
          new Response("seq,occurred_at\n1,2026-01-01T00:00:00Z\n", { status: 200, headers: { "Content-Type": "text/csv" } }),
        );
      }
      return Promise.resolve(jsonResponse({ entries: [] }));
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn().mockReturnValue("blob:mock"), revokeObjectURL: vi.fn() });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const user = userEvent.setup();

    renderWithProviders(<AuditLog />, { route: "/audit-log" });
    await screen.findByText(/no audit entries match/i);

    await user.click(screen.getByRole("button", { name: /export csv/i }));

    await waitFor(() => {
      const exportCall = fetchMock.mock.calls.find((c) => (c[0] as string).includes("/export"));
      expect(exportCall).toBeDefined();
      const headers = (exportCall![1] as RequestInit).headers as Record<string, string>;
      expect(headers["X-Dev-User-Email"]).toBe("alice@example.com");
    });
    expect(clickSpy).toHaveBeenCalled();
  });
});
