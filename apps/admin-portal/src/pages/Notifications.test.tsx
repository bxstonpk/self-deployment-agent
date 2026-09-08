import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Notifications } from "./Notifications";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const NOTIFICATIONS = [
  {
    id: "notif-1", category: "deployment_status", title: "overtime: deployment succeeded",
    detail: "Deployment dep-1 is now running.", resource_type: "deployment", resource_id: "dep-1",
    read_at: null, created_at: "2026-01-02T00:00:00Z",
  },
  {
    id: "notif-2", category: "approval_request", title: "overtime: production deployment awaiting approval",
    detail: "Deployment dep-2 is awaiting production approval.", resource_type: "deployment", resource_id: "dep-2",
    read_at: "2026-01-01T01:00:00Z", created_at: "2026-01-01T00:00:00Z",
  },
];

describe("Notifications", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders notifications returned by the API", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ notifications: NOTIFICATIONS })));

    renderWithProviders(<Notifications />, { route: "/notifications" });

    expect(await screen.findByText("overtime: deployment succeeded")).toBeInTheDocument();
    expect(screen.getByText("overtime: production deployment awaiting approval")).toBeInTheDocument();
  });

  it("only shows Mark read for unread notifications", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ notifications: NOTIFICATIONS })));

    renderWithProviders(<Notifications />, { route: "/notifications" });
    await screen.findByText("overtime: deployment succeeded");

    expect(screen.getAllByRole("button", { name: /mark read/i })).toHaveLength(1);
  });

  it("re-queries with unread_only when the checkbox is toggled", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ notifications: NOTIFICATIONS }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<Notifications />, { route: "/notifications" });
    await screen.findByText("overtime: deployment succeeded");

    await user.click(screen.getByLabelText(/unread only/i));

    await waitFor(() => {
      const lastCall = fetchMock.mock.calls.at(-1) as [string, RequestInit];
      expect(lastCall[0]).toContain("unread_only=true");
    });
  });

  it("marking a notification read calls the API and refreshes the list", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, opts?: RequestInit) => {
      if (opts?.method === "POST") {
        return Promise.resolve(jsonResponse({ ...NOTIFICATIONS[0], read_at: "2026-01-02T01:00:00Z" }));
      }
      return Promise.resolve(jsonResponse({ notifications: NOTIFICATIONS }));
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<Notifications />, { route: "/notifications" });
    await screen.findByText("overtime: deployment succeeded");

    await user.click(screen.getByRole("button", { name: /mark read/i }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find((c) => (c[1] as RequestInit)?.method === "POST");
      expect(postCall).toBeDefined();
      expect(postCall![0]).toContain("/notifications/notif-1/read");
    });
  });

  it("surfaces an API error instead of hanging on a blank loading state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: { code: "INTERNAL_ERROR", message: "database unreachable" } }, 500)),
    );

    renderWithProviders(<Notifications />, { route: "/notifications" });

    expect(await screen.findByText(/database unreachable/i)).toBeInTheDocument();
  });
});
