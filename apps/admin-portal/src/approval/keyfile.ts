// Signing an approval in the browser, with the approver's own key file.
//
// The key file is what `company-deploy approver keygen` wrote: an Ed25519 key
// as passphrase-encrypted PKCS#8 PEM (PBES2: PBKDF2-HMAC-SHA256 + AES-CBC).
// It is read from the file the approver picks, decrypted and used here, in
// memory, and never sent anywhere — only the signature is.
//
// Pure JavaScript (@noble), not WebCrypto: the Portal is served over plain
// http on the LAN, where browsers don't offer crypto.subtle at all.

import { cbc } from "@noble/ciphers/aes";
import * as ed from "@noble/ed25519";
import { pbkdf2 } from "@noble/hashes/pbkdf2";
import { sha1 } from "@noble/hashes/sha1";
import { sha256, sha512 } from "@noble/hashes/sha2";

// @noble/ed25519's synchronous API needs a SHA-512.
ed.etc.sha512Sync = (...m) => sha512(ed.etc.concatBytes(...m));

export class KeyFileError extends Error {}

// --- a minimal DER reader: just what PKCS#8 / PBES2 needs -------------------

interface Der {
  tag: number;
  body: Uint8Array;
  end: number; // offset just past this element
}

function readDer(buf: Uint8Array, at: number): Der {
  const tag = buf[at];
  let len = buf[at + 1];
  let off = at + 2;
  if (len & 0x80) {
    const n = len & 0x7f;
    if (n < 1 || n > 4) throw new KeyFileError("unsupported key file encoding");
    len = 0;
    for (let i = 0; i < n; i++) len = (len << 8) | buf[off + i];
    off += n;
  }
  if (off + len > buf.length) throw new KeyFileError("the key file is truncated");
  return { tag, body: buf.subarray(off, off + len), end: off + len };
}

function children(seq: Uint8Array): Der[] {
  const out: Der[] = [];
  let at = 0;
  while (at < seq.length) {
    const d = readDer(seq, at);
    out.push(d);
    at = d.end;
  }
  return out;
}

function oid(bytes: Uint8Array): string {
  const parts = [Math.floor(bytes[0] / 40), bytes[0] % 40];
  let v = 0;
  for (let i = 1; i < bytes.length; i++) {
    v = (v << 7) | (bytes[i] & 0x7f);
    if (!(bytes[i] & 0x80)) {
      parts.push(v);
      v = 0;
    }
  }
  return parts.join(".");
}

function uint(bytes: Uint8Array): number {
  let v = 0;
  for (const b of bytes) v = v * 256 + b;
  return v;
}

const OID = {
  pbes2: "1.2.840.113549.1.5.13",
  pbkdf2: "1.2.840.113549.1.5.12",
  hmacSHA1: "1.2.840.113549.2.7",
  hmacSHA256: "1.2.840.113549.2.9",
  aes128cbc: "2.16.840.1.101.3.4.1.2",
  aes256cbc: "2.16.840.1.101.3.4.1.42",
  ed25519: "1.3.101.112",
};

function pemBody(pem: string): { label: string; der: Uint8Array } {
  const m = pem.match(/-----BEGIN ([A-Z ]+)-----([\s\S]+?)-----END \1-----/);
  if (!m) throw new KeyFileError("this isn't a key file (no PEM block)");
  const b64 = m[2].replace(/\s+/g, "");
  const bin = atob(b64);
  const der = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) der[i] = bin.charCodeAt(i);
  return { label: m[1], der };
}

/** Decrypts an approver key file and returns its 32-byte Ed25519 seed. */
export function decryptKeyFile(pem: string, passphrase: string): Uint8Array {
  const { label, der } = pemBody(pem);
  if (label !== "ENCRYPTED PRIVATE KEY") {
    throw new KeyFileError("expected the passphrase-protected key from `company-deploy approver keygen`");
  }
  const [alg, encrypted] = children(readDer(der, 0).body);
  const [algOid, algParams] = children(alg.body);
  if (oid(algOid.body) !== OID.pbes2) throw new KeyFileError("unsupported key file encryption");
  const [kdf, enc] = children(algParams.body);
  const [kdfOid, kdfParams] = children(kdf.body);
  if (oid(kdfOid.body) !== OID.pbkdf2) throw new KeyFileError("unsupported key derivation");
  const kp = children(kdfParams.body);
  const salt = kp[0].body;
  const iterations = uint(kp[1].body);
  let prf = sha1; // PKCS#5's default when none is named
  for (const extra of kp.slice(2)) {
    if (extra.tag === 0x30) {
      const prfOid = oid(children(extra.body)[0].body);
      if (prfOid === OID.hmacSHA256) prf = sha256;
      else if (prfOid !== OID.hmacSHA1) throw new KeyFileError("unsupported key derivation hash");
    }
  }
  const [encOid, iv] = children(enc.body);
  const cipher = oid(encOid.body);
  const keyLen = cipher === OID.aes256cbc ? 32 : cipher === OID.aes128cbc ? 16 : 0;
  if (!keyLen) throw new KeyFileError("unsupported key file cipher");

  const key = pbkdf2(prf, new TextEncoder().encode(passphrase), salt, { c: iterations, dkLen: keyLen });
  let plain: Uint8Array;
  try {
    plain = cbc(key, iv.body).decrypt(encrypted.body);
  } catch {
    throw new KeyFileError("wrong passphrase");
  }
  // PrivateKeyInfo { version, AlgorithmIdentifier { ed25519 }, OCTET STRING { OCTET STRING seed } }
  try {
    const info = children(readDer(plain, 0).body);
    if (oid(children(info[1].body)[0].body) !== OID.ed25519) throw new KeyFileError("this isn't an Ed25519 approver key");
    const seed = readDer(info[2].body, 0).body;
    if (seed.length !== 32) throw new KeyFileError("unexpected key length");
    return seed;
  } catch (e) {
    if (e instanceof KeyFileError) throw e;
    throw new KeyFileError("wrong passphrase");
  }
}

function base64(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s);
}

/** The fingerprint the platform logs for a key: SHA256 of the raw public key, unpadded base64. */
export function fingerprint(seed: Uint8Array): string {
  return "SHA256:" + base64(sha256(ed.getPublicKey(seed))).replace(/=+$/, "");
}

/** One line, as the platform puts a reason into the signed text (domain.OneLine). */
export function oneLine(s: string): string {
  return s.split(/\s+/).filter(Boolean).join(" ");
}

/** Now, as the platform expects it: UTC, whole seconds. */
export function signedAtNow(now: Date = new Date()): string {
  return now.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** Fills the platform's signing template exactly as it does to verify. */
export function signedMessage(template: string, decision: string, reason: string, signedAt: string): Uint8Array {
  const text = template
    .split("{decision}").join(decision)
    .split("{reason}").join(oneLine(reason))
    .split("{signed_at}").join(signedAt);
  return new TextEncoder().encode(text);
}

/** Signs a decision; returns what POST /approvals/{id}/decision takes. */
export function signDecision(
  seed: Uint8Array,
  template: string,
  decision: "approve" | "reject",
  reason: string,
  now: Date = new Date(),
): { signed_at: string; signature: string } {
  const signedAt = signedAtNow(now);
  const signature = ed.sign(signedMessage(template, decision, reason, signedAt), seed);
  return { signed_at: signedAt, signature: base64(signature) };
}
