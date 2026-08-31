import { beforeEach, describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SignIn } from "./SignIn";
import { renderWithProviders } from "../test/utils";
import { loadIdentity } from "../identity";

describe("SignIn", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("does not sign in with an empty email (required field blocks submit)", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SignIn />);

    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(loadIdentity()).toBeNull();
  });

  it("saves the identity to localStorage on submit", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SignIn />);

    await user.type(screen.getByLabelText(/email/i), "alice@example.com");
    await user.type(screen.getByLabelText(/^name/i), "Alice Employee");
    await user.type(screen.getByLabelText(/department/i), "Engineering");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(loadIdentity()).toEqual({
      email: "alice@example.com",
      name: "Alice Employee",
      department: "Engineering",
    });
  });

  it("omits optional fields entirely when left blank, rather than saving empty strings", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SignIn />);

    await user.type(screen.getByLabelText(/email/i), "bob@example.com");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    expect(loadIdentity()).toEqual({ email: "bob@example.com" });
  });
});
