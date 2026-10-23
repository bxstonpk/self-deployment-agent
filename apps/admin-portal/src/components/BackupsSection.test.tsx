import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackupsSection } from "./BackupsSection";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const IDENTITY = { email: "alice@example.com", token: "cps_test" };
const FULL = {
  id: "full-1", kind: "full", trigger: "scheduled", status: "succeeded",
  stored_bytes: 20480, dump_bytes: 90000, taken_at: "2026-10-25T18:00:00Z",
};
const DIFF = {
  id: "diff-1", kind: "differential", base_backup_id: "full-1", trigger: "scheduled", status: "succeeded",
  stored_bytes: 300, dump_bytes: 90100, taken_at: "2026-10-26T18:00:00Z",
};
const OVERVIEW = {
  policy: "weekly",
  schedule: { full_weekday: "Sunday", hour: 1, timezone: "Asia/Bangkok", retention: "x" },
  backups: [DIFF, FULL],
};

describe("BackupsSection", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("describes the schedule and lists the backups", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(OVERVIEW))));
    render(<BackupsSection identity={IDENTITY} applicationId="app-1" applicationName="leave" />);

    expect(await screen.findByText(/full backup every Sunday/)).toBeInTheDocument();
    expect(screen.getByText("differential")).toBeInTheDocument();
    expect(screen.getByText("20.0 KB")).toBeInTheDocument();
  });

  it("only restores once the application's exact name is typed", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string, options?: RequestInit) =>
      Promise.resolve(
        url.endsWith("/restore") && options?.method === "POST"
          ? jsonResponse({ restored_backup_id: "diff-1", pre_restore_backup: { ...DIFF, id: "safety-123456", trigger: "pre_restore" } })
          : jsonResponse(OVERVIEW),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<BackupsSection identity={IDENTITY} applicationId="app-1" applicationName="leave" />);

    const row = (await screen.findByText("differential")).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: /restore/i }));
    const dialog = screen.getByRole("dialog", { name: /confirm restore/i });
    const confirmButton = within(dialog).getByRole("button", { name: /restore this backup/i });
    expect(confirmButton).toBeDisabled();
    await user.type(within(dialog).getByRole("textbox"), "leav");
    expect(confirmButton).toBeDisabled();
    await user.type(within(dialog).getByRole("textbox"), "e");
    await user.click(confirmButton);

    expect(await screen.findByRole("status")).toHaveTextContent(/backed up first \(safety-1/);
    const call = fetchMock.mock.calls.find((c) => (c[0] as string).endsWith("/backups/diff-1/restore"));
    expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({ confirm: "leave" });
  });

  it("takes a backup on request", async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, options?: RequestInit) =>
      Promise.resolve(options?.method === "POST" ? jsonResponse({ backup: { ...DIFF, trigger: "manual" } }, 201) : jsonResponse(OVERVIEW)),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<BackupsSection identity={IDENTITY} applicationId="app-1" applicationName="leave" />);

    await user.click(await screen.findByRole("button", { name: /back up now/i }));
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(/Took a differential backup/));
  });
});
