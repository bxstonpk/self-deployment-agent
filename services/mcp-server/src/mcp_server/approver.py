"""The platform administrator's side of signed approvals (DEC-017).

A deployment that needs approval (production, by default) goes ahead only
on a decision signed with the administrator's Ed25519 private key. The key
is generated and used here, on the administrator's own machine. The
platform only ever sees the public key and the signature, so nothing
stored on the platform, nothing an AI agent holds, and no stolen password
can approve a deployment.

    company-deploy approver keygen      # once: make the key, print the public half for the platform
    company-deploy approvals            # what is waiting
    company-deploy approve <deployment> # review it — images, scans, the requester's test report — and sign

The private key file is encrypted with a passphrase (PKCS#8, scrypt) and
written owner-only. Keep a copy offline: losing it means nobody can
approve until a new public key is configured.
"""

from __future__ import annotations

import base64
import os
import stat
from datetime import datetime, timezone
from pathlib import Path

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

PASSPHRASE_ENV = "COMPANY_DEPLOY_APPROVER_PASSPHRASE"


def default_key_path() -> Path:
    return Path.home() / ".company-deploy" / "approver_ed25519.pem"


def public_key_line(email: str, key: Ed25519PrivateKey) -> str:
    """The PLATFORM_APPROVER_KEYS entry for this key."""
    raw = key.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
    return f"{email}=ed25519:{base64.b64encode(raw).decode()}"


def fingerprint(key: Ed25519PrivateKey) -> str:
    """The same fingerprint the platform logs and records: SHA256 of the
    raw public key, base64, unpadded."""
    import hashlib

    raw = key.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
    return "SHA256:" + base64.b64encode(hashlib.sha256(raw).digest()).decode().rstrip("=")


def generate(path: Path, passphrase: bytes) -> Ed25519PrivateKey:
    if path.exists():
        raise FileExistsError(f"{path} already exists; refusing to overwrite an approver key")
    if len(passphrase) < 12:
        raise ValueError("use a passphrase of at least 12 characters")
    key = Ed25519PrivateKey.generate()
    pem = key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
                            serialization.BestAvailableEncryption(passphrase))
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, stat.S_IRUSR | stat.S_IWUSR)
    with os.fdopen(fd, "wb") as f:
        f.write(pem)
    return key


def load(path: Path, passphrase: bytes) -> Ed25519PrivateKey:
    key = serialization.load_pem_private_key(path.read_bytes(), password=passphrase)
    if not isinstance(key, Ed25519PrivateKey):
        raise ValueError(f"{path} is not an Ed25519 key")
    return key


def signed_at_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).strftime("%Y-%m-%dT%H:%M:%SZ")


def one_line(s: str) -> str:
    """How the platform puts a reason into the signed text (domain.OneLine)."""
    return " ".join(s.split())


def message(template: str, decision: str, reason: str, signed_at: str) -> bytes:
    """Fill the platform's signing template exactly as it does to verify."""
    return (template.replace("{decision}", decision)
            .replace("{reason}", one_line(reason))
            .replace("{signed_at}", signed_at)).encode()


def sign(key: Ed25519PrivateKey, template: str, decision: str, reason: str) -> tuple[str, str]:
    signed_at = signed_at_now()
    sig = key.sign(message(template, decision, reason, signed_at))
    return signed_at, base64.b64encode(sig).decode()
