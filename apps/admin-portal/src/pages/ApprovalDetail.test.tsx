import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import * as ed from "@noble/ed25519";
import fixture from "../approval/__fixtures__/signing.json";
import { decryptKeyFile, signedMessage } from "../approval/keyfile";
import { sha256Hex } from "../approval/tarball";
import { ApprovalDetail } from "./ApprovalDetail";
import { Approvals } from "./Approvals";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const archive = Uint8Array.from(atob(fixture.archive_b64), (c) => c.charCodeAt(0));

const REQUEST = {
  deployment_id: "dep-1",
  application_id: "app-1",
  application_name: "leave",
  environment: "dev",
  build_id: "b1",
  images: { api: "platform-build/leave-api:1" },
  requested_by: "somchai@sti-th.com",
  requested_at: "2026-10-24T09:00:00Z",
  status: "pending_approval",
  decision: "pending",
  scans: { api: { passed: true, critical: 0, high: 1 } },
  test_report: "go test ./...: ok",
  test_report_sha256: "x",
  review_document: "# leave\n\n## Structure\n\n| Path | Purpose |\n|---|---|\n| `api/main.go` | the HTTP server |\n",
  review_sha256: "y",
  source_sha256: sha256Hex(archive),
  source_bytes: archive.length,
  signing_template: fixture.template,
};

function mockApi(overrides: Partial<typeof REQUEST> = {}) {
  const req = { ...REQUEST, ...overrides };
  const fetchMock = vi.fn().mockImplementation((url: string, opts?: RequestInit) => {
    if (url.endsWith("/approvals/dep-1/source")) {
      return Promise.resolve(new Response(archive.slice().buffer, { status: 200, headers: { "Content-Type": "application/gzip" } }));
    }
    if (url.endsWith("/approvals/dep-1/decision") && opts?.method === "POST") {
      return Promise.resolve(jsonResponse({ deployment: { status: "running" }, signer_fingerprint: fixture.fingerprint }));
    }
    if (url.endsWith("/approvals/dep-1")) return Promise.resolve(jsonResponse(req));
    if (url.endsWith("/approvals/pending")) return Promise.resolve(jsonResponse({ pending: [req] }));
    return Promise.resolve(jsonResponse({}));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderDetail() {
  return renderWithProviders(
    <Routes>
      <Route path="/approvals/:deploymentId" element={<ApprovalDetail />} />
    </Routes>,
    { route: "/approvals/dep-1" },
  );
}

describe("Approvals", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("lists what's waiting, linking to the review", async () => {
    mockApi();
    renderWithProviders(<Approvals />, { route: "/approvals" });
    expect(await screen.findByText("leave")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Review" })).toHaveAttribute("href", "/approvals/dep-1");
  });

  it("shows REVIEW.md rendered, and every file of the verified source", async () => {
    mockApi();
    const user = userEvent.setup();
    renderDetail();

    expect(await screen.findByRole("heading", { name: "Structure" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "the HTTP server" })).toBeInTheDocument();
    expect(await screen.findByText(/files, verified/)).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /Files/ }));
    await user.click(screen.getByRole("button", { name: "frontend/src/หน้าแรก.jsx" }));
    expect(screen.getByLabelText("Contents of frontend/src/หน้าแรก.jsx")).toHaveTextContent("// ภาษาไทย");
    await user.click(screen.getByRole("button", { name: "api/logo.png" }));
    expect(screen.getByText("Binary file — not shown.")).toBeInTheDocument();
  });

  it("won't approve a source that doesn't match what would be signed", async () => {
    mockApi({ source_sha256: "0".repeat(64) });
    renderDetail();
    expect(await screen.findByText("Doesn't match what you'd sign")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign and approve" })).toBeDisabled();
  });

  it("signs in the browser with the key file and sends only the signature", async () => {
    const fetchMock = mockApi();
    const user = userEvent.setup();
    renderDetail();
    await screen.findByText(/files, verified/);

    const keyFile = new File([fixture.pem], "approver_ed25519.pem", { type: "text/plain" });
    await user.upload(screen.getByLabelText("Approver key file"), keyFile);
    await user.type(screen.getByLabelText("Key passphrase"), fixture.passphrase);
    await user.click(screen.getByRole("button", { name: "Sign and approve" }));

    expect(await screen.findByRole("status")).toHaveTextContent(`Signed with ${fixture.fingerprint}`);
    const post = fetchMock.mock.calls.find(([url, opts]) => String(url).endsWith("/decision") && (opts as RequestInit)?.method === "POST");
    const body = JSON.parse((post![1] as RequestInit).body as string);
    expect(Object.keys(body).sort()).toEqual(["decision", "reason", "signature", "signed_at"]);
    expect(JSON.stringify(body)).not.toContain("PRIVATE KEY");

    // The signature verifies against the key's public half, over the platform's own template.
    const seed = decryptKeyFile(fixture.pem, fixture.passphrase);
    const sig = Uint8Array.from(atob(body.signature), (c) => c.charCodeAt(0));
    const ok = await ed.verifyAsync(sig, signedMessage(fixture.template, "approve", "", body.signed_at), ed.getPublicKey(seed));
    expect(ok).toBe(true);
  });

  it("says so when the passphrase is wrong, and sends nothing", async () => {
    const fetchMock = mockApi();
    const user = userEvent.setup();
    renderDetail();
    await screen.findByText(/files, verified/);
    await user.upload(screen.getByLabelText("Approver key file"), new File([fixture.pem], "k.pem"));
    await user.type(screen.getByLabelText("Key passphrase"), "wrong passphrase");
    await user.click(screen.getByRole("button", { name: "Sign and approve" }));
    expect(await screen.findByText("wrong passphrase")).toBeInTheDocument();
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith("/decision"))).toBe(false),
    );
  });

  it("needs a reason to reject", async () => {
    mockApi();
    const user = userEvent.setup();
    renderDetail();
    await screen.findByText(/files, verified/);
    await user.click(screen.getByRole("radio", { name: "Reject" }));
    await user.upload(screen.getByLabelText("Approver key file"), new File([fixture.pem], "k.pem"));
    await user.type(screen.getByLabelText("Key passphrase"), fixture.passphrase);
    await user.click(screen.getByRole("button", { name: "Sign and reject" }));
    expect(await screen.findByText(/Say why you're rejecting it/)).toBeInTheDocument();
  });

  it("says whether the platform ran the tests or the requester only reported them", async () => {
    mockApi({ test_report: "Tests run by the platform (not reported by the requester)\nresult: passed\n" });
    const first = renderDetail();
    expect(await screen.findByText("Run by the platform")).toBeInTheDocument();
    first.unmount();

    mockApi({ test_report: "--- from the requester (not run by the platform) ---\ngo test: ok\n" });
    renderDetail();
    expect(await screen.findByText("Requester's report only")).toBeInTheDocument();
  });
});
