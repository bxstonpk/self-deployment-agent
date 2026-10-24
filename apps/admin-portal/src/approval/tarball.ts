// Reading a build's source archive in the browser, so the approver can open
// every file: gunzip, then the tar entries (ustar, with the PAX headers
// Python's tarfile writes for long or non-ASCII names).

import { gunzipSync } from "fflate";
import { sha256 } from "@noble/hashes/sha2";

export interface SourceFile {
  path: string;
  data: Uint8Array;
}

function cstr(block: Uint8Array, start: number, len: number): string {
  let end = start;
  while (end < start + len && block[end] !== 0) end++;
  return new TextDecoder().decode(block.subarray(start, end));
}

function octal(block: Uint8Array, start: number, len: number): number {
  const s = cstr(block, start, len).trim();
  return s ? parseInt(s, 8) : 0;
}

function paxPath(data: Uint8Array): string | null {
  const text = new TextDecoder().decode(data);
  for (const rec of text.split("\n")) {
    const m = rec.match(/^\d+ path=(.*)$/);
    if (m) return m[1];
  }
  return null;
}

/** The regular files in a .tar.gz, in archive order. */
export function readTarGz(archive: Uint8Array): SourceFile[] {
  const tar = gunzipSync(archive);
  const files: SourceFile[] = [];
  let at = 0;
  let nextPath: string | null = null;
  while (at + 512 <= tar.length) {
    const header = tar.subarray(at, at + 512);
    if (header.every((b) => b === 0)) break;
    const size = octal(header, 124, 12);
    const type = String.fromCharCode(header[156] || 48);
    const body = tar.subarray(at + 512, at + 512 + size);
    at += 512 + Math.ceil(size / 512) * 512;

    if (type === "x") {
      nextPath = paxPath(body);
      continue;
    }
    if (type === "g") continue;
    const prefix = cstr(header, 345, 155);
    const name = nextPath ?? (prefix ? `${prefix}/${cstr(header, 0, 100)}` : cstr(header, 0, 100));
    nextPath = null;
    if (type === "0" || type === "\0" || type === "7") {
      files.push({ path: name.replace(/^\.\//, ""), data: body.slice() });
    }
  }
  return files;
}

export function sha256Hex(data: Uint8Array): string {
  return Array.from(sha256(data), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** Text if it looks like text (UTF-8, no NULs), otherwise null. */
export function asText(data: Uint8Array): string | null {
  if (data.subarray(0, 8000).includes(0)) return null;
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(data);
  } catch {
    return null;
  }
}
