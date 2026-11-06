import { describe, expect, it } from "vitest";
import { highlightLines, languageOf } from "./highlight";

describe("highlightLines", () => {
  it("gives one line per source line, a multi-line comment closed and reopened on each", () => {
    const lines = highlightLines("/* one\ntwo */\nconst a = 1;\n", "src/a.ts");
    expect(lines).toHaveLength(3);
    expect(lines[0]).toBe('<span class="hljs-comment">/* one</span>');
    expect(lines[1]).toBe('<span class="hljs-comment">two */</span>');
    expect(lines[2]).toContain('<span class="hljs-keyword">const</span>');
  });

  it("escapes markup in the source, highlighted or not", () => {
    for (const path of ["index.html", "notes.txt"]) {
      const html = highlightLines('<script>alert("x")</script>', path).join("");
      expect(html).not.toContain("<script>");
      expect(html).toContain("&lt;");
    }
  });

  it("knows the company stack's files", () => {
    expect(languageOf("frontend/src/App.jsx")).toBe("javascript");
    expect(languageOf("api/main.go")).toBe("go");
    expect(languageOf("api/Dockerfile")).toBe("dockerfile");
    expect(languageOf("api/app.py")).toBe("python");
    expect(languageOf("LICENSE")).toBeNull();
  });
});
