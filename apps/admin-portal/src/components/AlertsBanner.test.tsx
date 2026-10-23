import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { AlertsBanner } from "./AlertsBanner";

const IDENTITY = { email: "alice@example.com", token: "cps_test" };

function respond(body: unknown, status = 200) {
  vi.stubGlobal("fetch", vi.fn().mockImplementation(() =>
    Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }))));
}

describe("AlertsBanner", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows what is firing, worst first in its own words", async () => {
    respond({
      active: [{ service: "api", rule: "error_rate", level: "critical", value: 0.3,
        message: "30.0% of requests to api failed with a 5xx over the last 5m0s (30 of 100)", started_at: "2026-10-23T03:00:00Z" }],
      recent: [], thresholds: {},
    });
    render(<AlertsBanner identity={IDENTITY} applicationId="app-1" />);
    const banner = await screen.findByRole("alert", { name: /active alerts/i });
    expect(banner).toHaveTextContent("1 alert is firing");
    expect(banner).toHaveTextContent("critical");
    expect(banner).toHaveTextContent("30 of 100");
  });

  it("shows nothing when all is well, or to someone who isn't an owner", async () => {
    respond({ active: [], recent: [], thresholds: {} });
    const { container, unmount } = render(<AlertsBanner identity={IDENTITY} applicationId="app-1" />);
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
    unmount();

    respond({ error: { code: "not_found", message: "application not found" } }, 404);
    const second = render(<AlertsBanner identity={IDENTITY} applicationId="app-1" />);
    await new Promise((r) => setTimeout(r, 20));
    expect(second.container).toBeEmptyDOMElement();
  });
});
