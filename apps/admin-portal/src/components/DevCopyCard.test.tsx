import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DevCopyCard } from "./DevCopyCard";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const COPY = {
  application_id: "dev-1",
  name: "leave-dev",
  copy_of: "leave",
  copy_of_id: "app-1",
  url: "http://192.168.10.25/leave-dev/",
  created_at: "2026-10-24T09:00:00Z",
  test_data: true,
  test_data_note: "2026-10-24 09:10 claude: 3 ใบลาทดสอบ",
};

describe("DevCopyCard", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows nothing when there is no dev copy", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: { code: "no_dev_copy", message: "none" } }, 404));
    vi.stubGlobal("fetch", fetchMock);
    const { container } = renderWithProviders(<DevCopyCard applicationId="app-1" />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("links it, shows Claude's test data, and resets only after confirming", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, opts?: RequestInit) =>
      Promise.resolve(opts?.method === "DELETE" ? jsonResponse({ removed: COPY.name }) : jsonResponse({ dev_copy: COPY })),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<DevCopyCard applicationId="app-1" />);
    expect(await screen.findByRole("link", { name: COPY.name })).toHaveAttribute("href", COPY.url);
    expect(screen.getByText(/3 ใบลาทดสอบ/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Reset dev copy" }));
    expect(fetchMock.mock.calls.some(([, o]) => (o as RequestInit | undefined)?.method === "DELETE")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Remove it" }));
    await waitFor(() => expect(screen.queryByRole("link", { name: COPY.name })).not.toBeInTheDocument());
    expect(fetchMock.mock.calls.some(([url, o]) => String(url).endsWith("/applications/app-1/dev") && o?.method === "DELETE")).toBe(true);
  });
});
