import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Departments } from "./Departments";
import { RegisterApplication } from "./RegisterApplication";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SUMMARY = {
  departments: [
    { id: "d1", name: "Innovation", status: "active", users: 3, applications: 2, created_at: "2026-10-23T08:00:00Z" },
    { id: "d2", name: "ฝ่ายบัญชี", status: "active", users: 1, applications: 0, created_at: "2026-10-23T09:00:00Z" },
  ],
};

function postBodies(fetchMock: ReturnType<typeof vi.fn>, path: string): unknown[] {
  return fetchMock.mock.calls
    .filter(([url, opts]) => String(url).endsWith(path) && (opts as RequestInit | undefined)?.method === "POST")
    .map(([, opts]) => JSON.parse((opts as RequestInit).body as string));
}

describe("Departments", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("lists every department with its people and applications", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(SUMMARY)));
    renderWithProviders(<Departments />, { route: "/departments" });

    const row = (await screen.findByText("Innovation")).closest("tr")!;
    expect(within(row).getByText("3")).toBeInTheDocument();
    expect(within(row).getByText("2")).toBeInTheDocument();
    expect(screen.getByText("ฝ่ายบัญชี")).toBeInTheDocument();
  });

  it("adds a department and reloads the list", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, opts?: RequestInit) => {
      if (opts?.method === "POST") {
        return Promise.resolve(
          jsonResponse({ department: { ID: "d3", Name: "Human Resources", CostCenterCode: "", Status: "active" } }),
        );
      }
      return Promise.resolve(jsonResponse(SUMMARY));
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<Departments />, { route: "/departments" });
    await screen.findByText("Innovation");

    await user.type(screen.getByLabelText("Department name"), "Human Resources");
    await user.click(screen.getByRole("button", { name: "Add department" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Added Human Resources.");
    expect(postBodies(fetchMock, "/departments")).toEqual([{ name: "Human Resources" }]);
    await waitFor(() =>
      expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/departments/summary")).length).toBe(2),
    );
  });

  it("says so when the department already exists, whatever its case", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation((_url: string, opts?: RequestInit) =>
        Promise.resolve(
          opts?.method === "POST"
            ? jsonResponse({ department: { ID: "d1", Name: "Innovation", CostCenterCode: "", Status: "active" } })
            : jsonResponse(SUMMARY),
        ),
      ),
    );
    const user = userEvent.setup();
    renderWithProviders(<Departments />, { route: "/departments" });
    await screen.findByText("Innovation");

    await user.type(screen.getByLabelText("Department name"), "innovation");
    await user.click(screen.getByRole("button", { name: "Add department" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Innovation already exists.");
  });
});

describe("RegisterApplication: any department", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  const APP = {
    id: "app-1", name: "leave", description: "", owning_department_id: "d9", created_by: "u1",
    lifecycle_status: "draft", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
  };

  function mockApi(): ReturnType<typeof vi.fn> {
    const fetchMock = vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
      if (url.endsWith("/departments") && opts?.method === "POST") {
        return Promise.resolve(jsonResponse({ department: { ID: "d9", Name: "Human Resources", CostCenterCode: "", Status: "active" } }));
      }
      if (url.endsWith("/departments")) {
        return Promise.resolve(
          jsonResponse({
            departments: [
              { ID: "d1", Name: "Engineering", CostCenterCode: "", Status: "active" },
              { ID: "d2", Name: "Innovation", CostCenterCode: "", Status: "active" },
            ],
          }),
        );
      }
      if (url.endsWith("/auth/me")) {
        return Promise.resolve(jsonResponse({ user: { id: "u1", email: "a@sti-th.com", full_name: "", department_id: "d2" }, is_admin: false }));
      }
      return Promise.resolve(jsonResponse(APP));
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  it("starts on the employee's own department", async () => {
    mockApi();
    renderWithProviders(<RegisterApplication />, { route: "/applications/new" });
    await waitFor(() => expect(screen.getByLabelText("Owning department")).toHaveValue("d2"));
  });

  it("can register an application for a department that doesn't exist yet", async () => {
    const fetchMock = mockApi();
    const user = userEvent.setup();
    renderWithProviders(<RegisterApplication />, { route: "/applications/new" });
    await screen.findByRole("option", { name: "Innovation" });

    await user.type(screen.getByLabelText(/^name/i), "leave");
    await user.selectOptions(screen.getByLabelText("Owning department"), "+ New department…");
    await user.type(screen.getByLabelText("New department name"), "Human Resources");
    await user.click(screen.getByRole("button", { name: /register/i }));

    await waitFor(() => expect(postBodies(fetchMock, "/applications")).toHaveLength(1));
    expect(postBodies(fetchMock, "/departments")).toEqual([{ name: "Human Resources" }]);
    expect(postBodies(fetchMock, "/applications")[0]).toMatchObject({ name: "leave", owning_department_id: "d9" });
  });
});
