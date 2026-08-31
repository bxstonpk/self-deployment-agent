import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatusBadge } from "./StatusBadge";

describe("StatusBadge", () => {
  it("renders the status text", () => {
    render(<StatusBadge status="running" />);
    expect(screen.getByText("running")).toBeInTheDocument();
  });

  it("maps a known status to its semantic class", () => {
    render(<StatusBadge status="running" />);
    expect(screen.getByText("running")).toHaveClass("badge-success");
  });

  it("maps failed to the danger class", () => {
    render(<StatusBadge status="failed" />);
    expect(screen.getByText("failed")).toHaveClass("badge-danger");
  });

  it("falls back to neutral for an unrecognized status rather than throwing", () => {
    render(<StatusBadge status="some-future-status" />);
    expect(screen.getByText("some-future-status")).toHaveClass("badge-neutral");
  });
});
