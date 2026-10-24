import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Standards } from "./Standards";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const STANDARDS = {
  rules: ["Start every application with start_project."],
  stacks: [
    { kind: "frontend", name: "react", status: "active" },
    { kind: "backend", name: "go", status: "active" },
    { kind: "backend", name: "php", status: "blocked" },
    { kind: "database", name: "mysql", status: "active" },
  ],
  dependency_rules: [
    { id: "r1", ecosystem: "npm", package: "left-pad", reason: "unmaintained", created_by: "admin@sti-th.com", created_at: "2026-10-24T08:00:00Z" },
  ],
  profile_required_to_build: true,
  exception_kinds: ["dependency", "stack", "other"],
};

const PENDING = {
  id: "e1",
  application_id: "a1",
  application_name: "overtime",
  kind: "dependency",
  subject: "npm:moment",
  reason: "the vendor SDK needs it",
  status: "pending",
  requested_by: "somchai@sti-th.com",
  requested_at: "2026-10-24T09:00:00Z",
  decided_by: "",
  decision_reason: "",
  decided_at: null,
};

function mockApi() {
  const fetchMock = vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
    const method = opts?.method ?? "GET";
    if (url.endsWith("/standards") && method === "GET") return Promise.resolve(jsonResponse(STANDARDS));
    if (url.includes("/standards/exceptions?status=pending")) return Promise.resolve(jsonResponse({ exceptions: [PENDING] }));
    if (url.includes("/standards/exceptions?status=")) return Promise.resolve(jsonResponse({ exceptions: [] }));
    if (url.endsWith("/decision") && method === "POST") {
      return Promise.resolve(jsonResponse({ exception: { ...PENDING, status: "approved" } }));
    }
    if (url.endsWith("/standards/dependency-rules") && method === "POST") {
      return Promise.resolve(jsonResponse({ dependency_rule: { id: "r2" } }, 201));
    }
    if (url.includes("/standards/dependency-rules/") && method === "DELETE") return Promise.resolve(jsonResponse({ removed: true }));
    return Promise.resolve(jsonResponse({}));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function calls(fetchMock: ReturnType<typeof vi.fn>, method: string, path: string) {
  return fetchMock.mock.calls.filter(
    ([url, opts]) => String(url).includes(path) && ((opts as RequestInit | undefined)?.method ?? "GET") === method,
  );
}

describe("Standards", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs("admin@sti-th.com");
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows the standard, its rules and what's waiting", async () => {
    mockApi();
    renderWithProviders(<Standards />, { route: "/standards" });
    expect(await screen.findByText("npm:moment")).toBeInTheDocument();
    expect(screen.getByText("the vendor SDK needs it")).toBeInTheDocument();
    expect(screen.getByText("left-pad")).toBeInTheDocument();
    expect(screen.getByText(/Frontend: react · API: go · Database:/)).toBeInTheDocument();
    expect(screen.queryByText(/php/)).not.toBeInTheDocument();
  });

  it("approves an exception", async () => {
    const fetchMock = mockApi();
    const user = userEvent.setup();
    renderWithProviders(<Standards />, { route: "/standards" });
    await screen.findByText("npm:moment");
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(calls(fetchMock, "POST", "/standards/exceptions/e1/decision")).toHaveLength(1));
    const body = JSON.parse((calls(fetchMock, "POST", "/decision")[0][1] as RequestInit).body as string);
    expect(body).toEqual({ decision: "approve", reason: "" });
  });

  it("won't reject without a reason", async () => {
    const fetchMock = mockApi();
    const user = userEvent.setup();
    renderWithProviders(<Standards />, { route: "/standards" });
    await screen.findByText("npm:moment");
    await user.click(screen.getByRole("button", { name: "Reject" }));
    expect(await screen.findByText(/Say why you're rejecting it/)).toBeInTheDocument();
    expect(calls(fetchMock, "POST", "/decision")).toHaveLength(0);

    await user.type(screen.getByLabelText("Decision reason for overtime npm:moment"), "use date-fns");
    await user.click(screen.getByRole("button", { name: "Reject" }));
    await waitFor(() => expect(calls(fetchMock, "POST", "/decision")).toHaveLength(1));
    const body = JSON.parse((calls(fetchMock, "POST", "/decision")[0][1] as RequestInit).body as string);
    expect(body).toEqual({ decision: "reject", reason: "use date-fns" });
  });

  it("adds and removes a library rule", async () => {
    const fetchMock = mockApi();
    const user = userEvent.setup();
    renderWithProviders(<Standards />, { route: "/standards" });
    await screen.findByText("left-pad");
    await user.selectOptions(screen.getByLabelText("Ecosystem"), "pypi");
    await user.type(screen.getByLabelText("Package"), "PyYAML");
    await user.type(screen.getByLabelText("Why"), "use json");
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    await waitFor(() => expect(calls(fetchMock, "POST", "/standards/dependency-rules")).toHaveLength(1));
    const body = JSON.parse((calls(fetchMock, "POST", "/standards/dependency-rules")[0][1] as RequestInit).body as string);
    expect(body).toEqual({ ecosystem: "pypi", package: "PyYAML", reason: "use json" });

    await user.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(calls(fetchMock, "DELETE", "/standards/dependency-rules/r1")).toHaveLength(1));
  });
});
