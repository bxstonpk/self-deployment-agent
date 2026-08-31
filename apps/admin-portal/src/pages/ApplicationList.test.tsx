import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApplicationList } from "./ApplicationList";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const APPS = [
  { id: "app-1", name: "overtime", description: "", owning_department_id: "d1", created_by: "u1", lifecycle_status: "running", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
  { id: "app-2", name: "payroll", description: "", owning_department_id: "d1", created_by: "u1", lifecycle_status: "suspended", created_at: "2026-01-02T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
];

describe("ApplicationList", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders applications returned by the API", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ applications: APPS })));

    renderWithProviders(<ApplicationList />, { route: "/applications" });

    expect(await screen.findByText("overtime")).toBeInTheDocument();
    expect(screen.getByText("payroll")).toBeInTheDocument();
  });

  it("sends the signed-in identity as dev-auth headers", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ applications: [] }));
    vi.stubGlobal("fetch", fetchMock);

    renderWithProviders(<ApplicationList />, { route: "/applications" });

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const [, options] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = options.headers as Record<string, string>;
    expect(headers["X-Dev-User-Email"]).toBe("alice@example.com");
  });

  it("filters the list by search text", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ applications: APPS })));
    const user = userEvent.setup();

    renderWithProviders(<ApplicationList />, { route: "/applications" });
    await screen.findByText("overtime");

    await user.type(screen.getByLabelText(/search applications/i), "pay");

    expect(screen.queryByText("overtime")).not.toBeInTheDocument();
    expect(screen.getByText("payroll")).toBeInTheDocument();
  });

  it("filters the list by lifecycle status", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ applications: APPS })));
    const user = userEvent.setup();

    renderWithProviders(<ApplicationList />, { route: "/applications" });
    await screen.findByText("overtime");

    await user.selectOptions(screen.getByLabelText(/filter by lifecycle status/i), "suspended");

    expect(screen.queryByText("overtime")).not.toBeInTheDocument();
    expect(screen.getByText("payroll")).toBeInTheDocument();
  });

  it("surfaces an API error instead of hanging on a blank loading state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: { code: "INTERNAL_ERROR", message: "database unreachable" } }, 500),
      ),
    );

    renderWithProviders(<ApplicationList />, { route: "/applications" });

    expect(await screen.findByText(/database unreachable/i)).toBeInTheDocument();
  });
});
