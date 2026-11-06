import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SourceHistoryCard } from "./SourceHistoryCard";
import { renderWithProviders, signInAs } from "../test/utils";
import fixture from "../approval/__fixtures__/signing.json";

const archive = Uint8Array.from(atob(fixture.archive_b64), (c) => c.charCodeAt(0));

const BUILDS = [
  {
    build_id: "b-dev-0001", application_id: "app-dev", application_name: "leave-dev", dev_copy: true, status: "failed",
    started_at: "2026-11-06T09:00:00Z", triggered_by: "alice@example.com", kept: true, source_bytes: 4096, deployments: [],
  },
  {
    build_id: "b-prod-0001", application_id: "app-1", application_name: "leave", dev_copy: false, status: "succeeded",
    started_at: "2026-11-05T09:00:00Z", triggered_by: "alice@example.com", kept: true, source_bytes: 2048,
    deployments: [{ id: "dep-1", environment: "production", status: "running" }],
  },
  {
    build_id: "b-old-0001", application_id: "app-1", application_name: "leave", dev_copy: false, status: "succeeded",
    started_at: "2026-10-01T09:00:00Z", triggered_by: "alice@example.com", kept: false, source_bytes: 0, deployments: [],
  },
];

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

let paths: string[] = [];

function mockApi(list: () => Response) {
  paths = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = new URL(String(input)).pathname;
      paths.push(path);
      if (path === "/applications/app-1/sources") return Promise.resolve(list());
      if (path === "/applications/app-1/sources/b-dev-0001") return Promise.resolve(new Response(archive.slice()));
      return Promise.resolve(jsonResponse({ error: { code: "not_found", message: "nope" } }, 404));
    }),
  );
}

describe("SourceHistoryCard", () => {
  beforeEach(() => signInAs());
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("lists the application's and its dev copy's builds, and opens a dev build's files", async () => {
    mockApi(() => jsonResponse({ builds: BUILDS }));
    renderWithProviders(<SourceHistoryCard applicationId="app-1" />);

    const rows = await screen.findAllByRole("row");
    expect(rows).toHaveLength(4); // header + 3 builds
    expect(rows[1]).toHaveTextContent("leave-dev");
    expect(rows[1]).toHaveTextContent("dev");
    expect(rows[2]).toHaveTextContent("production");
    expect(within(rows[3]).getByText("Not kept")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Dev copy" }));
    expect(screen.getAllByRole("row")).toHaveLength(2);
    await user.click(screen.getByRole("tab", { name: "Application" }));
    expect(screen.getAllByRole("row")).toHaveLength(3);
    await user.click(screen.getByRole("tab", { name: "All" }));

    await user.click(within(screen.getAllByRole("row")[1]).getByRole("button", { name: /View the source of leave-dev/ }));
    expect(await screen.findByRole("button", { name: "Download all (.tar.gz)" })).toBeInTheDocument();
    expect(paths).toContain("/applications/app-1/sources/b-dev-0001");
    expect(screen.getByLabelText("Filter files")).toBeInTheDocument();
  });

  it("is hidden from someone who may not read the source", async () => {
    mockApi(() => jsonResponse({ error: { code: "forbidden", message: "no" } }, 403));
    const { container } = renderWithProviders(<SourceHistoryCard applicationId="app-1" />);
    await vi.waitFor(() => expect(paths).toContain("/applications/app-1/sources"));
    await new Promise((r) => setTimeout(r, 0));
    expect(container).toBeEmptyDOMElement();
  });
});
