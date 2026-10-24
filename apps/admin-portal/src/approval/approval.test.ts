import { describe, expect, it } from "vitest";
import fixture from "./__fixtures__/signing.json";
import { decryptKeyFile, fingerprint, KeyFileError, oneLine, signDecision, signedAtNow, signedMessage } from "./keyfile";
import { asText, readTarGz, sha256Hex } from "./tarball";

// The fixtures were made by the same Python code `company-deploy` signs with
// (scripts in the PR), so these prove the browser produces exactly what the
// CLI does — Ed25519 is deterministic, so the signatures must be identical.

function b64(s: string): Uint8Array {
  const bin = atob(s);
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

describe("approver key file", () => {
  it("decrypts the key the CLI wrote and has the same fingerprint", () => {
    const seed = decryptKeyFile(fixture.pem, fixture.passphrase);
    expect(seed).toHaveLength(32);
    expect(fingerprint(seed)).toBe(fixture.fingerprint);
  });

  it("signs exactly what the CLI signs", () => {
    const seed = decryptKeyFile(fixture.pem, fixture.passphrase);
    const out = signDecision(seed, fixture.template, "approve", fixture.reason, new Date(fixture.signed_at));
    expect(out.signed_at).toBe(fixture.signed_at);
    expect(out.signature).toBe(fixture.python_signature);
  });

  it("refuses a wrong passphrase", () => {
    expect(() => decryptKeyFile(fixture.pem, "not the passphrase")).toThrow(KeyFileError);
  });

  it("refuses something that isn't the encrypted key", () => {
    expect(() => decryptKeyFile("hello", "x")).toThrow(KeyFileError);
    expect(() =>
      decryptKeyFile("-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA\n-----END PUBLIC KEY-----", "x"),
    ).toThrow(/passphrase-protected key/);
  });

  it("fills the template the way the platform does", () => {
    const text = new TextDecoder().decode(signedMessage("d: {decision}\nr: {reason}\nt: {signed_at}\n", "reject", " a \n b ", "T"));
    expect(text).toBe("d: reject\nr: a b\nt: T\n");
    expect(oneLine("  x\t y \n")).toBe("x y");
    expect(signedAtNow(new Date("2026-10-24T09:00:00.789Z"))).toBe("2026-10-24T09:00:00Z");
  });
});

describe("source archive", () => {
  const archive = b64(fixture.archive_b64);

  it("lists every file, including long and Thai names", () => {
    const paths = readTarGz(archive).map((f) => f.path);
    expect(paths).toContain("api/main.go");
    expect(paths).toContain("frontend/src/App.jsx");
    expect(paths).toContain("api/" + "deep/".repeat(30) + "long.txt");
    expect(paths).toContain("frontend/src/หน้าแรก.jsx");
  });

  it("reads text and recognises binary", () => {
    const files = Object.fromEntries(readTarGz(archive).map((f) => [f.path, f.data]));
    expect(asText(files["api/main.go"])).toBe("package main\n");
    expect(asText(files["frontend/src/หน้าแรก.jsx"])).toBe("// ภาษาไทย\n");
    expect(asText(files["api/logo.png"])).toBeNull();
  });

  it("hashes like the platform", () => {
    expect(sha256Hex(new TextEncoder().encode("abc"))).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    );
  });
});
