import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Handoffs } from "./Handoffs";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const READY = {
  id: "pkg-1",
  deployment_id: "d1234567-aaaa",
  trigger: "promotion",
  status: "ready",
  file_name: "report-handoff-20261116-0900.tar",
  size_bytes: 300 * 1024 * 1024,
  sha256: "ab12",
  created_at: "2026-11-16T09:00:00Z",
  completed_at: "2026-11-16T09:05:00Z",
};

describe("Handoffs", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs("admin@example.com");
  });
  afterEach(() => vi.unstubAllGlobals());

  it("downloads a ready package and makes one again", async () => {
    const created: string[] = [];
    const fetchMock = vi.fn().mockImplementation((url: string, options?: RequestInit) => {
      if (url.endsWith("/handoffs/pkg-1/file")) {
        return Promise.resolve(new Response(new Blob(["tar bytes"]), { status: 200 }));
      }
      if (options?.method === "POST") {
        created.push(url);
        return Promise.resolve(jsonResponse({ handoff: { ...READY, id: "pkg-2", status: "building" } }, 202));
      }
      return Promise.resolve(
        jsonResponse({
          handoffs: [
            {
              application_id: "app-1",
              application_name: "report",
              ready: READY,
              latest: { id: "pkg-0", deployment_id: "d0", trigger: "manual", status: "failed", error: "no space left", created_at: "2026-11-16T10:00:00Z" },
            },
          ],
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const createObjectURL = vi.fn(() => "blob:handoff");
    vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL: vi.fn() });
    const user = userEvent.setup();
    renderWithProviders(<Handoffs />, { route: "/handoff" });

    const row = (await screen.findByText("report")).closest("tr")!;
    expect(within(row).getByText("report-handoff-20261116-0900.tar")).toBeInTheDocument();
    expect(within(row).getByText("300 MB")).toBeInTheDocument();
    expect(within(row).getByText(/no space left/)).toBeInTheDocument();

    await user.click(within(row).getByRole("button", { name: /download \.tar/i }));
    await waitFor(() => expect(screen.getByText(/downloaded\. The download is recorded/)).toBeInTheDocument());
    expect(createObjectURL).toHaveBeenCalled();

    await user.click(within(row).getByRole("button", { name: /make again/i }));
    await waitFor(() => expect(created).toHaveLength(1));
    expect(created[0]).toMatch(/\/applications\/app-1\/handoffs$/);
  });

  it("tells someone who isn't an administrator or approver", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ error: { code: "forbidden", message: "no" } }, 403)),
    );
    renderWithProviders(<Handoffs />, { route: "/handoff" });
    expect(await screen.findByText(/Only administrators and approvers/)).toBeInTheDocument();
  });
});
