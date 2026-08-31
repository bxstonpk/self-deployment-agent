import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RegisterApplication } from "./RegisterApplication";
import { renderWithProviders, signInAs } from "../test/utils";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("RegisterApplication", () => {
  beforeEach(() => {
    localStorage.clear();
    signInAs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("normalizes the raw PascalCase /departments response into the select options", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          departments: [{ ID: "d1", Name: "Engineering", CostCenterCode: "ENG-01", Status: "active" }],
        }),
      ),
    );

    renderWithProviders(<RegisterApplication />, { route: "/applications/new" });

    expect(await screen.findByRole("option", { name: "Engineering" })).toBeInTheDocument();
  });

  it("submits registration with the selected department's real UUID, not its display name", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.endsWith("/departments")) {
        return Promise.resolve(
          jsonResponse({ departments: [{ ID: "d1", Name: "Engineering", CostCenterCode: "", Status: "active" }] }),
        );
      }
      return Promise.resolve(
        jsonResponse({
          id: "app-1",
          name: "overtime",
          description: "",
          owning_department_id: "d1",
          created_by: "u1",
          lifecycle_status: "draft",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<RegisterApplication />, { route: "/applications/new" });
    await screen.findByRole("option", { name: "Engineering" });

    await user.type(screen.getByLabelText(/^name/i), "overtime");
    await user.click(screen.getByRole("button", { name: /register/i }));

    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find(([, opts]) => (opts as RequestInit | undefined)?.method === "POST");
      expect(postCall).toBeDefined();
      const body = JSON.parse((postCall![1] as RequestInit).body as string);
      expect(body.owning_department_id).toBe("d1");
      expect(body.name).toBe("overtime");
    });
  });

  it("surfaces a registration failure (e.g. name already taken) instead of navigating away", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.endsWith("/departments")) {
        return Promise.resolve(
          jsonResponse({ departments: [{ ID: "d1", Name: "Engineering", CostCenterCode: "", Status: "active" }] }),
        );
      }
      return Promise.resolve(
        jsonResponse({ error: { code: "CONFLICT", message: "application name already registered" } }, 409),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();

    renderWithProviders(<RegisterApplication />, { route: "/applications/new" });
    await screen.findByRole("option", { name: "Engineering" });

    await user.type(screen.getByLabelText(/^name/i), "overtime");
    await user.click(screen.getByRole("button", { name: /register/i }));

    expect(await screen.findByText(/already registered/i)).toBeInTheDocument();
  });
});
