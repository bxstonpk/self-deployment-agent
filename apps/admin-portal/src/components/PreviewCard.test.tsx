import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PreviewCard } from "./PreviewCard";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const PREVIEW = {
  application_id: "pv-1",
  name: "leave-preview-a1b2",
  previews: "leave",
  previews_id: "app-1",
  url: "http://192.168.10.25/leave-preview-a1b2/",
  created_at: "2026-10-24T09:00:00Z",
  expires_at: "2026-10-25T09:00:00Z",
};

describe("PreviewCard", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows nothing when there is no preview", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: { code: "no_preview", message: "none" } }, 404));
    vi.stubGlobal("fetch", fetchMock);
    const { container } = renderWithProviders(<PreviewCard applicationId="app-1" />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("links the preview and stops it", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, opts?: RequestInit) =>
      Promise.resolve(opts?.method === "DELETE" ? jsonResponse({ stopped: PREVIEW.name }) : jsonResponse({ preview: PREVIEW })),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<PreviewCard applicationId="app-1" />);
    expect(await screen.findByRole("link", { name: PREVIEW.name })).toHaveAttribute("href", PREVIEW.url);
    await user.click(screen.getByRole("button", { name: "Stop preview" }));
    await waitFor(() => expect(screen.queryByRole("link", { name: PREVIEW.name })).not.toBeInTheDocument());
    expect(fetchMock.mock.calls.some(([url, o]) => String(url).endsWith("/applications/app-1/preview") && o?.method === "DELETE")).toBe(true);
  });
});
