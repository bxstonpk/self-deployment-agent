"""Review before approval: what Claude sends with a deployment that waits for
approval, and what `company-deploy review` lays out for the approver.

The platform owner (2026-10-24): every file is reviewed before anything goes
up, alongside a REVIEW.md that Claude writes — the workflow, the structure
and the path of every file, and what changed.
"""

from __future__ import annotations

import hashlib
import io
import os
import tarfile
from pathlib import Path, PurePosixPath

# What the requester's Claude writes at the project root.
REVIEW_FILE = "REVIEW.md"
MAX_REVIEW_BYTES = 256 * 1024


class ReviewError(Exception):
    pass


def read_review_document(project_path: str) -> str | None:
    """The project's REVIEW.md (any case), or None if there isn't one."""
    root = Path(project_path)
    if not root.is_dir():
        return None
    for entry in root.iterdir():
        if entry.is_file() and entry.name.lower() == REVIEW_FILE.lower():
            data = entry.read_bytes()
            if len(data) > MAX_REVIEW_BYTES:
                raise ReviewError(f"{entry.name} is larger than 256 KiB; keep it to what a reviewer needs")
            # One line ending everywhere: the same REVIEW.md written on Windows
            # and on a Mac is the same document, with the same signed hash.
            text = data.decode("utf-8", errors="replace").replace("\r\n", "\n")
            return text if text.strip() else None
    return None


def sha256_hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def safe_extract(archive: bytes, dest: Path) -> list[str]:
    """Extracts a source archive for reading, refusing anything that could
    land outside dest: absolute paths, "..", links and devices. Returns the
    files written, as archive paths."""
    dest = dest.resolve()
    written: list[str] = []
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
        for member in tar.getmembers():
            name = PurePosixPath(member.name)
            if name.is_absolute() or ".." in name.parts or str(name).startswith(("/", "\\")):
                raise ReviewError(f"refusing an archive entry outside the folder: {member.name}")
            if not (member.isfile() or member.isdir()):
                raise ReviewError(f"refusing a link or special file in the archive: {member.name}")
            target = (dest / Path(*name.parts)).resolve()
            if dest not in target.parents and target != dest:
                raise ReviewError(f"refusing an archive entry outside the folder: {member.name}")
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            src = tar.extractfile(member)
            if src is None:
                continue
            with open(target, "wb") as out:
                out.write(src.read())
            written.append(str(name))
    return sorted(written)


def tree(paths: list[str]) -> str:
    """The files as an indented tree, for the approver's terminal."""
    lines: list[str] = []
    seen: set[tuple[str, ...]] = set()
    for p in sorted(paths):
        parts = PurePosixPath(p).parts
        for depth in range(1, len(parts)):
            key = parts[:depth]
            if key not in seen:
                seen.add(key)
                lines.append("  " * (depth - 1) + parts[depth - 1] + "/")
        lines.append("  " * (len(parts) - 1) + parts[-1])
    return os.linesep.join(lines)
