"""Packs a project directory on this machine into the build archive the
Platform API expects: a gzip-compressed tar with one top-level directory per
service named in deployment.yaml (see services/platform-api's
build_service.go).

This is what lets `deploy_application(project_path=...)` work without the
agent base64-encoding a tarball into its own transcript — a multi-megabyte
string through the model for every deploy. The MCP server runs on the same
machine as Claude Code, so it reads the folder itself. It reads nothing
outside the given project directory, and only the service folders
deployment.yaml names.

What is never packed:
- dependencies and build output (node_modules, build/, dist/, virtualenvs,
  caches): the platform installs and builds from the lockfiles itself;
- version control (.git);
- local secrets: `.env` and `.env.*` files (but `.env.example` is kept),
  private keys and certificates. A credential belongs in the platform's
  secret store, not in an image. The names of anything skipped for this
  reason are reported, so the employee can be told to set them as secrets.
"""

from __future__ import annotations

import fnmatch
import io
import os
import tarfile
from dataclasses import dataclass, field
from pathlib import Path

# The Platform API refuses a larger archive (handlers_build.go).
MAX_ARCHIVE_BYTES = 50 * 1024 * 1024

# Only names that are unambiguously generated or tooling — never a name a
# project might plausibly use for its own source.
EXCLUDED_DIRS = {
    "node_modules", ".git", ".hg", ".svn", "build", "dist", ".next", ".nuxt",
    "__pycache__", ".venv", "venv", ".pytest_cache", ".mypy_cache", ".ruff_cache",
    ".tox", "coverage", ".cache", ".idea", ".vscode",
}
SECRET_PATTERNS = (".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*", "*.keystore")
SECRET_ALLOWED = (".env.example", ".env.sample", ".env.template")
NOISE_PATTERNS = (".DS_Store", "Thumbs.db", "*.log")


class PackError(Exception):
    pass


@dataclass
class PackSummary:
    services: list[str]
    files: int = 0
    uncompressed_bytes: int = 0
    archive_bytes: int = 0
    skipped_secret_files: list[str] = field(default_factory=list)

    def as_dict(self) -> dict:
        return {
            "services": self.services,
            "files": self.files,
            "uncompressed_bytes": self.uncompressed_bytes,
            "archive_bytes": self.archive_bytes,
            "skipped_secret_files": self.skipped_secret_files,
        }


def _is_secret(name: str) -> bool:
    if name in SECRET_ALLOWED:
        return False
    return any(fnmatch.fnmatch(name, p) for p in SECRET_PATTERNS)


def _is_noise(name: str) -> bool:
    return any(fnmatch.fnmatch(name, p) for p in NOISE_PATTERNS)


def pack_project(project_path: str, service_names: list[str]) -> tuple[bytes, PackSummary]:
    if not project_path or not os.path.isabs(project_path):
        raise PackError("project_path must be an absolute path to the project directory")
    root = Path(project_path).resolve()
    if not root.is_dir():
        raise PackError(f"project_path {project_path!r} is not a directory")
    if not service_names:
        raise PackError("the application's deployment.yaml declares no services")
    missing = [s for s in service_names if not (root / s).is_dir()]
    if missing:
        raise PackError(
            f"each service in deployment.yaml needs a folder of the same name in the project; "
            f"missing: {', '.join(missing)} (looked in {root})"
        )

    summary = PackSummary(services=list(service_names))
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for service in service_names:
            service_root = (root / service).resolve()
            for dirpath, dirnames, filenames in os.walk(service_root, followlinks=False):
                dirnames[:] = sorted(d for d in dirnames if d not in EXCLUDED_DIRS)
                for name in sorted(filenames):
                    path = Path(dirpath) / name
                    rel = path.relative_to(service_root).as_posix()
                    if path.is_symlink():
                        # A link could point anywhere on this machine.
                        continue
                    if _is_secret(name):
                        summary.skipped_secret_files.append(f"{service}/{rel}")
                        continue
                    if _is_noise(name):
                        continue
                    info = tar.gettarinfo(str(path), arcname=f"{service}/{rel}")
                    # Ownership and timestamps of this machine mean nothing
                    # to the build; normalised so the archive doesn't leak them.
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    info.mode = 0o755 if info.mode & 0o111 else 0o644
                    with open(path, "rb") as f:
                        tar.addfile(info, f)
                    summary.files += 1
                    summary.uncompressed_bytes += info.size
    archive = buf.getvalue()
    summary.archive_bytes = len(archive)
    if summary.files == 0:
        raise PackError("found no files to deploy in the service folders")
    if len(archive) > MAX_ARCHIVE_BYTES:
        raise PackError(
            f"the packed source is {len(archive) // (1024 * 1024)} MiB, over the platform's "
            f"{MAX_ARCHIVE_BYTES // (1024 * 1024)} MiB limit — check for large files that "
            "shouldn't be deployed (data, media, generated output)"
        )
    return archive, summary
