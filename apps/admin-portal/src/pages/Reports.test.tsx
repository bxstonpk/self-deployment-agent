import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Reports } from "./Reports";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const INVENTORY = [
  {
    application_id: "app-1", name: "overtime", department_id: "d1", department_name: "Engineering",
    lifecycle_status: "running", owner_user_ids: ["u1", "u2"], runtimes: ["go", "react"], environment: "production",
  },
  {
    application_id: "app-2", name: "payroll", department_id: "d2", department_name: "Finance",
    lifecycle_status: "draft", owner_user_ids: ["u1"], runtimes: [], environment: "",
  },
];

const ACTIVITY = {
  from: "2026-08-01T00:00:00Z",
  to: "2026-09-01T00:00:00Z",
  total: { succeeded: 7, failed: 2, rolled_back: 1 },
  by_environment: { dev: { succeeded: 5, failed: 2, rolled_back: 0 }, production: { succeeded: 2, failed: 0, rolled_back: 1 } },
  by_department: { Engineering: { succeeded: 7, failed: 2, rolled_back: 1 } },
};

function routeFetch(overrides: { inventory?: unknown; activity?: unknown } = {}) {
  return vi.fn().mockImplementation((url: string) => {
    if (url.includes("/reports/application-inventory")) {
      return Promise.resolve(jsonResponse(overrides.inventory ?? { applications: INVENTORY }));
    }
    return Promise.resolve(jsonResponse(overrides.activity ?? ACTIVITY));
  });
}

describe("Reports", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows the deployment outcome totals as a stat row", async () => {
    vi.stubGlobal("fetch", routeFetch());

    renderWithProviders(<Reports />, { route: "/reports" });

    // Scoped to the stat row's own accessible group — "Succeeded" etc. are
    // also column headers in the breakdown tables below.
    const totals = await screen.findByRole("group", { name: /deployment outcome totals/i });
    expect(within(totals).getByText("7")).toBeInTheDocument();
    expect(within(totals).getByText("2")).toBeInTheDocument();
    expect(within(totals).getByText("1")).toBeInTheDocument();
  });

  it("breaks activity down by environment and department", async () => {
    vi.stubGlobal("fetch", routeFetch());

    renderWithProviders(<Reports />, { route: "/reports" });

    // Scoped per table: "production" is also an Environment cell in the
    // inventory table further down the page.
    const byEnvironment = await screen.findByRole("table", { name: /environment breakdown/i });
    expect(within(byEnvironment).getByText("dev")).toBeInTheDocument();
    expect(within(byEnvironment).getByText("production")).toBeInTheDocument();

    const byDepartment = screen.getByRole("table", { name: /department breakdown/i });
    expect(within(byDepartment).getByText("Engineering")).toBeInTheDocument();
  });

  it("renders the application inventory with stack and environment", async () => {
    vi.stubGlobal("fetch", routeFetch());

    renderWithProviders(<Reports />, { route: "/reports" });

    expect(await screen.findByText("overtime")).toBeInTheDocument();
    expect(screen.getByText("go, react")).toBeInTheDocument();
    expect(screen.getByText("payroll")).toBeInTheDocument();
    // An application that has never been deployed says so rather than
    // rendering a blank cell.
    expect(screen.getByText("never deployed")).toBeInTheDocument();
  });

  it("sends the selected date range to the API", async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal("fetch", fetchMock);

    renderWithProviders(<Reports />, { route: "/reports" });

    await waitFor(() => {
      const activityCall = fetchMock.mock.calls.find((c) => (c[0] as string).includes("/deployment-activity"));
      expect(activityCall).toBeDefined();
      expect(activityCall![0]).toContain("from=");
      expect(activityCall![0]).toContain("to=");
    });
  });

  it("surfaces the available_from note when the range predates the data", async () => {
    vi.stubGlobal("fetch", routeFetch({ activity: { ...ACTIVITY, available_from: "2026-08-20T10:00:00Z" } }));

    renderWithProviders(<Reports />, { route: "/reports" });

    expect(await screen.findByText(/data only goes back to/i)).toBeInTheDocument();
  });

  // A half-edited range is normal, not exceptional. Both of these were
  // found by driving the real page: the backwards range showed up as a
  // 400 in the browser console, and clearing an input to retype it
  // crashed the component outright (empty string -> Invalid Date ->
  // toISOString() throws).
  it("does not query the API for an empty date, and says why", async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<Reports />, { route: "/reports" });
    await screen.findByRole("group", { name: /deployment outcome totals/i });
    const callsBefore = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/deployment-activity")).length;

    await user.clear(screen.getByLabelText(/report range start date/i));

    expect(await screen.findByText(/pick both a start and an end date/i)).toBeInTheDocument();
    const callsAfter = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/deployment-activity")).length;
    expect(callsAfter).toBe(callsBefore);
  });

  it("does not query the API for a backwards range, and says why", async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<Reports />, { route: "/reports" });
    await screen.findByRole("group", { name: /deployment outcome totals/i });
    const callsBefore = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/deployment-activity")).length;

    // Move the start date past the end date, exactly as a mid-edit user does.
    await user.clear(screen.getByLabelText(/report range start date/i));
    await user.type(screen.getByLabelText(/report range start date/i), "2099-01-01");

    expect(await screen.findByText(/start date is after the end date/i)).toBeInTheDocument();
    const callsAfter = fetchMock.mock.calls.filter((c) => (c[0] as string).includes("/deployment-activity")).length;
    expect(callsAfter).toBe(callsBefore);
  });

  it("surfaces an API error instead of hanging on a blank loading state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: { code: "INTERNAL_ERROR", message: "database unreachable" } }, 500)),
    );

    renderWithProviders(<Reports />, { route: "/reports" });

    expect(await screen.findByText(/database unreachable/i)).toBeInTheDocument();
  });
});
