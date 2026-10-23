"""This machine's API key for the Company Platform — the key it fetches
through a device approval before it may deploy (DEC-001/DEC-003 revised
2026-10-23, docs/17_Decision_Log.md).

Stored in one JSON file, by default ~/.company-deploy/credentials.json
(override with COMPANY_DEPLOY_CREDENTIALS), readable only by the user who
owns it. Both the MCP server and the `company-deploy` command line read
and write the same file, so signing in once — from either — is enough.

The key never leaves this file except in the Authorization header of a
request to the platform. No MCP tool returns it, so it never appears in an
AI agent's transcript.
"""

from __future__ import annotations

import json
import os
import stat
import tempfile
from dataclasses import asdict, dataclass
from pathlib import Path


@dataclass(frozen=True)
class StoredCredential:
    platform_url: str
    api_key: str
    key_id: str
    key_name: str
    email: str
    expires_at: str


def default_path() -> Path:
    override = os.environ.get("COMPANY_DEPLOY_CREDENTIALS", "").strip()
    if override:
        return Path(override).expanduser()
    return Path.home() / ".company-deploy" / "credentials.json"


def load(path: Path | None = None) -> StoredCredential | None:
    path = path or default_path()
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    try:
        return StoredCredential(**{k: str(raw[k]) for k in StoredCredential.__dataclass_fields__})
    except (KeyError, TypeError):
        return None


def for_platform(platform_url: str, path: Path | None = None) -> StoredCredential | None:
    """The stored key, if it was issued by this platform — a key for a
    different platform address is as good as none."""
    cred = load(path)
    if cred is None or cred.platform_url.rstrip("/") != platform_url.rstrip("/"):
        return None
    return cred


def save(cred: StoredCredential, path: Path | None = None) -> Path:
    """Writes atomically, owner-only. On Windows the mode bits only mark
    the file read-write; the protection there is the user profile
    directory's own ACL, which is where the default path lives."""
    path = path or default_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        os.chmod(path.parent, stat.S_IRWXU)
    except OSError:
        pass
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=".credentials-", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(asdict(cred), f, indent=2)
        os.chmod(tmp, stat.S_IRUSR | stat.S_IWUSR)
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise
    return path


def delete(path: Path | None = None) -> bool:
    path = path or default_path()
    try:
        path.unlink()
        return True
    except FileNotFoundError:
        return False
