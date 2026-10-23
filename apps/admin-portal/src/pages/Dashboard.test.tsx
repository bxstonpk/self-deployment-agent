import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Dashboard } from "./Dashboard";
import { formatBytes } from "../format";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const GB = 1024 ** 3;

const OVERVIEW = {
  generated_at: "2026-10-23T09:00:00Z",
  host: {
    available: true, cpu_count: 4, cpu_percent: 37.5, memory_used_bytes: 1.5 * GB, memory_total_bytes: 3 * GB,
    load_1: 2.31, load_5: 2.1, load_15: 1.9, uptime_seconds: 90_000, sampled_at: "2026-10-23T09:00:00Z",
  },
  host_history: [
    { at: "2026-10-23T08:59:50Z", cpu_percent: 20, memory_used_bytes: 1.4 * GB, memory_total_bytes: 3 * GB },
    { at: "2026-10-23T08:59:55Z", cpu_percent: 37.5, memory_used_bytes: 1.5 * GB, memory_total_bytes: 3 * GB },
  ],
  disk_used_percent: 14.2,
  traffic: [
    { minute: "2026-10-23T08:58:00Z", requests: 12, errors: 0 },
    { minute: "2026-10-23T08:59:00Z", requests: 40, errors: 3 },
  ],
  traffic_window_seconds: 300,
  applications: [
    { id: "a1", name: "leave-tracker", status: "running", department: "HR", owner_email: "somchai@sti-th.com",
      environments: "dev", instances: 2, cpu_percent: 3.2, memory_bytes: 210 * 1024 ** 2, requests: 120, errors: 0,
      latency_ms_avg: 18.4, url: "http://192.168.10.25/leave-tracker/" },
    { id: "a2", name: "overtime", status: "running", department: "Finance", owner_email: "malee@sti-th.com",
      environments: "dev, production", instances: 2, cpu_percent: 41, memory_bytes: 600 * 1024 ** 2, requests: 900,
      errors: 90, latency_ms_avg: 250, url: "http://192.168.10.25/overtime/" },
    { id: "a3", name: "draft-app", status: "draft", department: "IT", owner_email: "it@sti-th.com",
      environments: "", instances: 0, cpu_percent: 0, memory_bytes: 0, requests: 0, errors: 0, latency_ms_avg: 0, url: "" },
  ],
};

function appNamesInOrder(): string[] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("link")
    .map((a) => a.textContent ?? "");
}

describe("Dashboard", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows the host's CPU, memory, disk and traffic, and every application", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(OVERVIEW));
    vi.stubGlobal("fetch", fetchMock);

    renderWithProviders(<Dashboard />, { route: "/dashboard" });

    expect(await screen.findByText("leave-tracker")).toBeInTheDocument();
    expect(String(fetchMock.mock.calls[0][0])).toMatch(/\/admin\/overview$/);
    expect(screen.getByText("38%")).toBeInTheDocument(); // CPU 37.5 -> 38%
    expect(screen.getByText("4 cores · load 2.31")).toBeInTheDocument();
    expect(screen.getByText("1.5 GB of 3.0 GB used")).toBeInTheDocument();
    expect(screen.getByText("14%")).toBeInTheDocument(); // disk
    expect(screen.getByText(/requests in the latest minute · 3 errors/)).toBeInTheDocument();
    expect(screen.getByText("running of 3 · 4 instances")).toBeInTheDocument();
    expect(screen.getByText("somchai@sti-th.com · HR · dev")).toBeInTheDocument();
  });

  it("sorts by CPU, busiest first, and re-sorts when a column is clicked", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(OVERVIEW)));
    renderWithProviders(<Dashboard />, { route: "/dashboard" });
    await screen.findByText("leave-tracker");

    expect(appNamesInOrder()).toEqual(["overtime", "leave-tracker", "draft-app"]);

    await userEvent.click(screen.getByRole("button", { name: /^Application/ }));
    expect(appNamesInOrder()).toEqual(["draft-app", "leave-tracker", "overtime"]);
    expect(screen.getByRole("columnheader", { name: /Application/ })).toHaveAttribute("aria-sort", "ascending");
  });

  it("filters by name, owner or department", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(OVERVIEW)));
    renderWithProviders(<Dashboard />, { route: "/dashboard" });
    await screen.findByText("leave-tracker");

    await userEvent.type(screen.getByRole("searchbox", { name: "Filter applications" }), "finance");
    expect(appNamesInOrder()).toEqual(["overtime"]);
  });

  it("shows no traffic now when the last busy minute was a while ago", async () => {
    const quiet = { ...OVERVIEW, traffic: [{ minute: "2026-10-23T08:40:00Z", requests: 99, errors: 0 }] };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(quiet)));
    renderWithProviders(<Dashboard />, { route: "/dashboard" });
    await screen.findByText("leave-tracker");
    expect(screen.queryByText("99")).not.toBeInTheDocument();
  });

  it("marks a running application with no instance as idle (scaled to zero)", async () => {
    const idle = { ...OVERVIEW, applications: [{ ...OVERVIEW.applications[0], instances: 0 }] };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(idle)));
    renderWithProviders(<Dashboard />, { route: "/dashboard" });
    expect(await screen.findByText("0 · idle")).toBeInTheDocument();
  });

  it("tells a non-administrator the dashboard isn't for them", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: { code: "forbidden", message: "only platform administrators" } }, 403)),
    );
    renderWithProviders(<Dashboard />, { route: "/dashboard" });

    expect(await screen.findByRole("alert")).toHaveTextContent("Only platform administrators can see the dashboard.");
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("says when the host's readings aren't available", async () => {
    const noHost = { ...OVERVIEW, host: { ...OVERVIEW.host, available: false }, host_history: [] };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(noHost)));
    renderWithProviders(<Dashboard />, { route: "/dashboard" });

    await screen.findByText("leave-tracker");
    expect(screen.getAllByText("Host readings unavailable")).toHaveLength(2);
  });
});

describe("formatBytes", () => {
  it("uses MB below a gigabyte and GB above", () => {
    expect(formatBytes(0)).toBe("0 MB");
    expect(formatBytes(210 * 1024 ** 2)).toBe("210 MB");
    expect(formatBytes(1.5 * GB)).toBe("1.5 GB");
    expect(formatBytes(16 * GB)).toBe("16 GB");
  });
});
