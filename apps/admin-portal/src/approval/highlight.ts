// Syntax highlighting for the source viewer, line by line (so each line can
// carry its number), with only the languages the company standard uses.

import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const LANGUAGES = { bash, css, dockerfile, go, ini, javascript, json, markdown, python, sql, typescript, xml, yaml };
for (const [name, lang] of Object.entries(LANGUAGES)) hljs.registerLanguage(name, lang);

const BY_EXTENSION: Record<string, string> = {
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  ts: "typescript", tsx: "typescript", mts: "typescript",
  json: "json", css: "css", scss: "css",
  html: "xml", htm: "xml", xml: "xml", svg: "xml", vue: "xml",
  py: "python", go: "go", sql: "sql",
  yml: "yaml", yaml: "yaml", md: "markdown",
  sh: "bash", bash: "bash", toml: "ini", ini: "ini", cfg: "ini", env: "ini",
};

const BY_NAME: Record<string, string> = {
  dockerfile: "dockerfile", "go.mod": "go", "requirements.txt": "ini", ".gitignore": "bash", ".dockerignore": "bash",
};

/** The highlight.js language of a path, or null to show it plain. */
export function languageOf(path: string): string | null {
  const base = path.split("/").pop()!.toLowerCase();
  if (BY_NAME[base]) return BY_NAME[base];
  const dot = base.lastIndexOf(".");
  return dot >= 0 ? (BY_EXTENSION[base.slice(dot + 1)] ?? null) : null;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// Above this, highlighting a file would hold the page up; it's shown plain.
const MAX_HIGHLIGHT = 300_000;

/**
 * The file as one HTML string per line. highlight.js's output is escaped
 * text plus <span class="hljs-…"> only; a span open across a line break
 * (a multi-line comment or string) is closed at the end of the line and
 * opened again on the next, so every line stands alone.
 */
export function highlightLines(text: string, path: string): string[] {
  const lang = languageOf(path);
  let html: string;
  if (lang && text.length <= MAX_HIGHLIGHT) {
    try {
      html = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value;
    } catch {
      html = escapeHtml(text);
    }
  } else {
    html = escapeHtml(text);
  }
  const lines: string[] = [];
  const open: string[] = [];
  let line = "";
  const tag = /<span[^>]*>|<\/span>|\n/g;
  let at = 0;
  for (let m = tag.exec(html); m; m = tag.exec(html)) {
    line += html.slice(at, m.index);
    at = m.index + m[0].length;
    if (m[0] === "\n") {
      lines.push(line + "</span>".repeat(open.length));
      line = open.join("");
    } else if (m[0] === "</span>") {
      open.pop();
      line += m[0];
    } else {
      open.push(m[0]);
      line += m[0];
    }
  }
  line += html.slice(at);
  lines.push(line + "</span>".repeat(open.length));
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  return lines;
}
