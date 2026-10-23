import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { SignIn } from "./SignIn";
import { safeNext } from "../navigation";
import { renderWithProviders } from "../test/utils";
import { loadIdentity } from "../identity";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION = {
  token: "cps_from-the-server",
  expires_at: "2099-01-01T00:00:00Z",
  user: { id: "u1", email: "alice@example.com", full_name: "Alice Employee", department_id: "d1" },
};

describe("SignIn", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("does not call the API with an empty form (required fields block submit)", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<SignIn />);

    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(fetchMock).not.toHaveBeenCalled();
    expect(loadIdentity()).toBeNull();
  });

  it("exchanges email and password for a session and keeps only the session", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(SESSION));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithProviders(<SignIn />);

    await user.type(screen.getByLabelText(/email/i), "alice@example.com");
    await user.type(screen.getByLabelText(/password/i), "correct horse battery");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => expect(loadIdentity()).not.toBeNull());
    const [url, options] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toMatch(/\/auth\/login$/);
    expect(JSON.parse(options.body as string)).toEqual({ email: "alice@example.com", password: "correct horse battery" });
    expect(loadIdentity()).toEqual({
      email: "alice@example.com",
      name: "Alice Employee",
      token: "cps_from-the-server",
      expiresAt: "2099-01-01T00:00:00Z",
    });
    expect(localStorage.getItem("admin-portal.identity")).not.toContain("correct horse battery");
  });

  it("shows the platform's answer on a failed sign-in and stays signed out", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: { code: "invalid_credentials", message: "email or password is incorrect" } }, 401),
      ),
    );
    const user = userEvent.setup();
    renderWithProviders(<SignIn />);

    await user.type(screen.getByLabelText(/email/i), "alice@example.com");
    await user.type(screen.getByLabelText(/password/i), "wrong password");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent("email or password is incorrect");
    expect(loadIdentity()).toBeNull();
  });

  it("returns to the page that sent you, such as a machine's approval link", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(SESSION)));
    const user = userEvent.setup();
    renderWithProviders(
      <Routes>
        <Route path="/sign-in" element={<SignIn />} />
        <Route path="/device" element={<p>device page</p>} />
      </Routes>,
      { route: "/sign-in?next=%2Fdevice%3Fcode%3DBCDF-GHJK" },
    );

    await user.type(screen.getByLabelText(/email/i), "alice@example.com");
    await user.type(screen.getByLabelText(/password/i), "correct horse battery");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(await screen.findByText("device page")).toBeInTheDocument();
  });

  it("ignores a next address that leaves the portal", () => {
    expect(safeNext("https://evil.example/")).toBe("/applications");
    expect(safeNext("//evil.example/")).toBe("/applications");
    expect(safeNext(null)).toBe("/applications");
    expect(safeNext("/device?code=X")).toBe("/device?code=X");
  });
});
