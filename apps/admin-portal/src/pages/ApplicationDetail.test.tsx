import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { ApplicationDetail } from "./ApplicationDetail";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const APP = {
  id: "app-1", name: "overtime", description: "HR overtime tracker", owning_department_id: "dept-1",
  created_by: "user-1", lifecycle_status: "running", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
};

const SECRETS = [
  { name: "API_KEY", managed_by: "employee", version: 2, updated_by: "user-1", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z" },
  { name: "DATABASE_PASSWORD", managed_by: "platform", version: 1, updated_by: null, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
];

type LogLine = { timestamp: string; service: string; stream: string; message: string; deployment_id: string; instance: string };

const LOG_LINES: LogLine[] = [
  { timestamp: "2026-01-05T10:00:02Z", service: "api", stream: "stderr", message: "panic: cannot reach the database", deployment_id: "dep-1", instance: "b1c2d3e4f5a6" },
  { timestamp: "2026-01-05T10:00:01Z", service: "api", stream: "stdout", message: "using API_KEY=[REDACTED:API_KEY]", deployment_id: "dep-1", instance: "b1c2d3e4f5a6" },
];

const SECRET_VALUE = "sk-test-0f1e2d3c4b5a69788796a5b4c3d2e1f0";

type Call = { method: string; path: string; search?: string; body?: string };

// Routes by path, so the whole detail page can load. Everything this suite
// doesn't care about 404s — which the page already treats as "none yet".
function mockPlatformApi(
  opts: {
    app?: object; secretsForbidden?: boolean; deployments?: object[]; build?: object;
    putError?: { status: number; code: string; message: string };
    rotateError?: { status: number; code: string; message: string };
    logs?: LogLine[]; olderLogs?: LogLine[]; nextCursor?: string;
    logsError?: { status: number; code: string; message: string };
  } = {},
) {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const url = new URL(String(input));
      const path = url.pathname;
      const method = init?.method ?? "GET";
      calls.push({ method, path, search: url.search, body: typeof init?.body === "string" ? init.body : undefined });

      if (method === "PUT" && path.startsWith("/applications/app-1/secrets/")) {
        if (opts.putError) {
          const { status, code, message } = opts.putError;
          return Promise.resolve(jsonResponse({ error: { code, message } }, status));
        }
        const name = decodeURIComponent(path.split("/").pop() ?? "");
        return Promise.resolve(jsonResponse({
          name, managed_by: "employee", version: 3, updated_by: "user-1",
          created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z",
        }));
      }
      if (method === "POST" && path.endsWith("/rotate")) {
        if (opts.rotateError) {
          const { status, code, message } = opts.rotateError;
          return Promise.resolve(jsonResponse({ error: { code, message } }, status));
        }
        return Promise.resolve(jsonResponse({
          secret: {
            name: "DATABASE_PASSWORD", managed_by: "platform", version: 2, updated_by: null,
            created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-04T00:00:00Z",
          },
          restarted: true,
          note: "Running instances were restarted onto the new password.",
        }));
      }
      if (method === "DELETE" && path.startsWith("/applications/app-1/secrets/")) {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/applications/app-1/logs") {
        if (opts.logsError) {
          const { status, code, message } = opts.logsError;
          return Promise.resolve(jsonResponse({ error: { code, message } }, status));
        }
        // Filtering is the platform's job; the mock does it only so the page
        // under test gets the answer a real server would give.
        if (url.searchParams.get("cursor")) return Promise.resolve(jsonResponse({ entries: opts.olderLogs ?? [], next_cursor: null }));
        const contains = url.searchParams.get("contains");
        const entries = (opts.logs ?? LOG_LINES).filter((l) => !contains || l.message.includes(contains));
        return Promise.resolve(jsonResponse({ entries, next_cursor: opts.nextCursor ?? null }));
      }
      if (path === "/applications/app-1") return Promise.resolve(jsonResponse(opts.app ?? APP));
      if (path === "/applications/app-1/secrets") {
        return Promise.resolve(
          opts.secretsForbidden
            ? jsonResponse({ error: { code: "forbidden", message: "requester is not authorized to perform this action" } }, 403)
            : jsonResponse({ secrets: SECRETS }),
        );
      }
      if (path === "/applications/app-1/deployments" && opts.deployments) return Promise.resolve(jsonResponse(opts.deployments));
      if (path === "/applications/app-1/builds/latest" && opts.build) return Promise.resolve(jsonResponse(opts.build));
      return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "not found" } }, 404));
    }),
  );
  return calls;
}

function renderDetail() {
  renderWithProviders(
    <Routes>
      <Route path="/applications/:id" element={<ApplicationDetail />} />
    </Routes>,
    { route: "/applications/app-1" },
  );
}

async function secretsSection(): Promise<HTMLElement> {
  await screen.findByText("API_KEY");
  return screen.getByRole("heading", { name: "Secrets" }).closest("section") as HTMLElement;
}

function rowFor(section: HTMLElement, name: string): HTMLElement {
  const row = within(section).getAllByRole("row").find((r) => within(r).queryByText(name));
  if (!row) throw new Error(`no row for ${name}`);
  return row;
}

describe("ApplicationDetail — Secrets", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("lists secrets by name, manager and version", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await secretsSection();

    expect(within(rowFor(section, "API_KEY")).getByText("Owner")).toBeInTheDocument();
    expect(within(rowFor(section, "API_KEY")).getByText("2")).toBeInTheDocument();
    expect(within(rowFor(section, "DATABASE_PASSWORD")).getByText("Platform")).toBeInTheDocument();
  });

  it("offers Delete for an owner's secret but not for a platform-managed one", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await secretsSection();

    expect(within(rowFor(section, "API_KEY")).getByRole("button", { name: "Delete" })).toBeInTheDocument();
    // It has Rotate now (see the Rotate suite below) — what matters here is
    // that nothing offers to delete a secret the platform depends on.
    expect(within(rowFor(section, "DATABASE_PASSWORD")).queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("sends the value once, then forgets it", async () => {
    const calls = mockPlatformApi();
    const user = userEvent.setup();
    renderDetail();
    const section = await secretsSection();

    await user.type(within(section).getByLabelText("Secret name"), "PAYROLL_API_KEY");
    await user.type(within(section).getByLabelText("Secret value"), SECRET_VALUE);
    await user.click(within(section).getByRole("button", { name: "Save secret" }));

    expect(await screen.findByText(/Saved PAYROLL_API_KEY \(version 3\)/)).toBeInTheDocument();
    const puts = calls.filter((c) => c.method === "PUT");
    expect(puts).toHaveLength(1);
    expect(puts[0].path).toBe("/applications/app-1/secrets/PAYROLL_API_KEY");
    expect(JSON.parse(puts[0].body ?? "{}")).toEqual({ value: SECRET_VALUE });

    expect(within(section).getByLabelText("Secret value")).toHaveValue("");
    expect(within(section).getByLabelText("Secret name")).toHaveValue("");
    expect(document.body.textContent).not.toContain(SECRET_VALUE);
    expect(JSON.stringify({ ...localStorage })).not.toContain(SECRET_VALUE);
  });

  it("keeps what was typed when a save fails, and shows the real reason", async () => {
    mockPlatformApi({
      putError: {
        status: 400, code: "reserved_secret_name",
        message: "secret names starting with DATABASE_ or PLATFORM_ are reserved for values the platform injects itself",
      },
    });
    const user = userEvent.setup();
    renderDetail();
    const section = await secretsSection();

    await user.type(within(section).getByLabelText("Secret name"), "DATABASE_URL");
    await user.type(within(section).getByLabelText("Secret value"), "postgres-somewhere-else");
    await user.click(within(section).getByRole("button", { name: "Save secret" }));

    expect(await screen.findByText(/reserved_secret_name/)).toBeInTheDocument();
    expect(within(section).getByLabelText("Secret name")).toHaveValue("DATABASE_URL");
    expect(within(section).getByLabelText("Secret value")).toHaveValue("postgres-somewhere-else");
  });

  it("asks before deleting, and deletes only when confirmed", async () => {
    const calls = mockPlatformApi();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderDetail();
    const section = await secretsSection();

    await user.click(within(rowFor(section, "API_KEY")).getByRole("button", { name: "Delete" }));
    expect(confirm).toHaveBeenCalledOnce();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    confirm.mockReturnValue(true);
    await user.click(within(rowFor(section, "API_KEY")).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "DELETE" && c.path === "/applications/app-1/secrets/API_KEY")).toBe(true),
    );
  });

  it("does not accept new secrets for a deleted application", async () => {
    mockPlatformApi({ app: { ...APP, lifecycle_status: "deleted" } });
    renderDetail();
    const section = await secretsSection();

    expect(within(section).getByRole("button", { name: "Save secret" })).toBeDisabled();
  });

  it("keeps the value out of spell-check and autofill", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await secretsSection();

    const value = within(section).getByLabelText("Secret value");
    expect(value.tagName).toBe("TEXTAREA");
    expect(value).toHaveAttribute("spellcheck", "false");
    expect(value).toHaveAttribute("autocomplete", "off");
  });

  it("tells a non-owner the secrets are owners-only, rather than that there are none", async () => {
    mockPlatformApi({ secretsForbidden: true });
    renderDetail();

    expect(await screen.findByText(/Only this application's owners can see or change its secrets/)).toBeInTheDocument();
    expect(screen.queryByText("No secrets yet.")).toBeNull();
  });
});

describe("ApplicationDetail — Delete", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  async function lifecycleDelete(): Promise<HTMLElement> {
    const section = (await screen.findByRole("heading", { name: "Lifecycle actions" })).closest("section") as HTMLElement;
    return within(section).getByRole("button", { name: "Delete" });
  }

  it("offers Delete for a draft that never went live", async () => {
    mockPlatformApi({ app: { ...APP, lifecycle_status: "draft" } });
    renderDetail();
    expect(await lifecycleDelete()).toBeEnabled();
  });

  it("does not offer Delete while a previous version is still serving", async () => {
    // A rebuild leaves the application in Build with the old deployment live.
    mockPlatformApi({
      app: { ...APP, lifecycle_status: "build" },
      build: { id: "b-1", application_id: "app-1", status: "succeeded" },
      deployments: [{ id: "dep-1", application_id: "app-1", status: "running", environment: "dev", created_at: "2026-01-01T00:00:00Z" }],
    });
    renderDetail();
    const button = await lifecycleDelete();
    await waitFor(() => expect(button).toBeDisabled());
    expect(button.getAttribute("title")).toMatch(/never went live/);
  });
});

describe("ApplicationDetail — Rotate", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("offers Rotate for the platform-managed database password, not for an owner's secret", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await secretsSection();

    expect(within(rowFor(section, "DATABASE_PASSWORD")).getByRole("button", { name: "Rotate" })).toBeInTheDocument();
    expect(within(rowFor(section, "API_KEY")).queryByRole("button", { name: "Rotate" })).toBeNull();
  });

  it("rotates only after confirming, and reports the new version and what happened", async () => {
    const calls = mockPlatformApi();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderDetail();
    const section = await secretsSection();
    const rotate = within(rowFor(section, "DATABASE_PASSWORD")).getByRole("button", { name: "Rotate" });

    await user.click(rotate);
    expect(confirm).toHaveBeenCalledOnce();
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    confirm.mockReturnValue(true);
    await user.click(rotate);
    expect(await screen.findByText(/Rotated DATABASE_PASSWORD \(version 2\)\. Running instances were restarted/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      "/applications/app-1/secrets/DATABASE_PASSWORD/rotate",
    ]);
  });

  it("shows an incomplete rotation as an error, not a success", async () => {
    mockPlatformApi({
      rotateError: {
        status: 500, code: "rotation_incomplete",
        message: "the secret was rotated, but running instances could not all be restarted onto it",
      },
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const user = userEvent.setup();
    renderDetail();
    const section = await secretsSection();

    await user.click(within(rowFor(section, "DATABASE_PASSWORD")).getByRole("button", { name: "Rotate" }));
    expect(await screen.findByText(/rotation_incomplete/)).toBeInTheDocument();
    expect(screen.queryByText(/Rotated DATABASE_PASSWORD/)).toBeNull();
  });
});

describe("ApplicationDetail — Logs", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  async function logsSection(): Promise<HTMLElement> {
    await screen.findByText("panic: cannot reach the database");
    return screen.getByRole("heading", { name: "Logs" }).closest("section") as HTMLElement;
  }

  it("shows what the containers printed, newest first, saying which stream each line came from", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await logsSection();

    const lines = within(section).getAllByRole("listitem");
    expect(lines[0]).toHaveTextContent("panic: cannot reach the database");
    // In words, not colour alone.
    expect(lines[0]).toHaveTextContent("err");
    expect(lines[1]).toHaveTextContent("out");
  });

  it("shows a secret exactly as the platform redacted it, and never the value", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await logsSection();

    expect(within(section).getByText("using API_KEY=[REDACTED:API_KEY]")).toBeInTheDocument();
  });

  it("asks the platform to search, rather than filtering the page it already has", async () => {
    const calls = mockPlatformApi();
    const user = userEvent.setup();
    renderDetail();
    const section = await logsSection();

    await user.type(within(section).getByLabelText("Search log text"), "panic");
    await user.click(within(section).getByRole("button", { name: "Search" }));

    await waitFor(() =>
      expect(calls.some((c) => c.path === "/applications/app-1/logs" && (c.search ?? "").includes("contains=panic"))).toBe(true),
    );
    expect(within(section).queryByText("using API_KEY=[REDACTED:API_KEY]")).toBeNull();
  });

  it("loads older lines with the platform's own cursor, keeping the ones already on screen", async () => {
    const calls = mockPlatformApi({
      nextCursor: "1757563506123456.42",
      olderLogs: [{ timestamp: "2026-01-05T09:59:00Z", service: "api", stream: "stdout", message: "an older line", deployment_id: "dep-1", instance: "b1c2d3e4f5a6" }],
    });
    const user = userEvent.setup();
    renderDetail();
    const section = await logsSection();

    await user.click(within(section).getByRole("button", { name: "Load older lines" }));

    expect(await within(section).findByText("an older line")).toBeInTheDocument();
    expect(within(section).getByText("panic: cannot reach the database")).toBeInTheDocument();
    expect(calls.some((c) => (c.search ?? "").includes("cursor=1757563506123456.42"))).toBe(true);
  });

  it("offers no paging when the platform reports no older lines", async () => {
    mockPlatformApi();
    renderDetail();
    const section = await logsSection();

    expect(within(section).queryByRole("button", { name: "Load older lines" })).toBeNull();
  });

  it("says nothing has run yet for an application that never deployed, and asks the platform for nothing", async () => {
    const calls = mockPlatformApi({ app: { ...APP, lifecycle_status: "draft" } });
    renderDetail();

    await screen.findByRole("heading", { name: "Logs" });
    expect(screen.getByText(/Nothing has run yet/)).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/applications/app-1/logs")).toBe(false);
  });

  // The platform answers 404 for "no such application" and "not yours"
  // alike, so it never tells a stranger an application exists. Here the
  // application is already on screen, so repeating "not found" would only
  // confuse the person reading it.
  it("tells a non-owner the logs are owners-only, not that the application is missing", async () => {
    mockPlatformApi({ logsError: { status: 404, code: "not_found", message: "application not found" } });
    renderDetail();

    const heading = await screen.findByRole("heading", { name: "Logs" });
    const section = heading.closest("section") as HTMLElement;
    expect(await within(section).findByText("Only this application's owners can read its logs.")).toBeInTheDocument();
    expect(within(section).queryByText("application not found")).toBeNull();
  });

  it("reports the platform's own reason for any other failure", async () => {
    mockPlatformApi({ logsError: { status: 500, code: "internal_error", message: "failed to read logs" } });
    renderDetail();

    const heading = await screen.findByRole("heading", { name: "Logs" });
    const section = heading.closest("section") as HTMLElement;
    expect(await within(section).findByText("failed to read logs")).toBeInTheDocument();
  });
});
