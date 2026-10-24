import asyncio
import io
import tarfile

import pytest

from mcp_server import cli, review


def _archive(entries: dict[str, bytes], links: dict[str, str] | None = None) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for name, data in entries.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
        for name, target in (links or {}).items():
            info = tarfile.TarInfo(name)
            info.type = tarfile.SYMTYPE
            info.linkname = target
            tar.addfile(info)
    return buf.getvalue()


def test_read_review_document_finds_it_in_any_case(tmp_path):
    assert review.read_review_document(str(tmp_path)) is None
    (tmp_path / "Review.MD").write_text("# app\n", encoding="utf-8")
    assert review.read_review_document(str(tmp_path)) == "# app\n"


def test_read_review_document_ignores_an_empty_one_and_refuses_a_huge_one(tmp_path):
    (tmp_path / "REVIEW.md").write_text("  \n", encoding="utf-8")
    assert review.read_review_document(str(tmp_path)) is None
    (tmp_path / "REVIEW.md").write_bytes(b"x" * (review.MAX_REVIEW_BYTES + 1))
    with pytest.raises(review.ReviewError):
        review.read_review_document(str(tmp_path))


def test_safe_extract_writes_the_files(tmp_path):
    files = review.safe_extract(_archive({"api/main.go": b"package main\n", "frontend/src/App.jsx": b"x"}), tmp_path)
    assert files == ["api/main.go", "frontend/src/App.jsx"]
    assert (tmp_path / "api" / "main.go").read_text() == "package main\n"


@pytest.mark.parametrize("name", ["../escape.txt", "api/../../escape.txt", "/etc/passwd"])
def test_safe_extract_refuses_paths_outside_the_folder(tmp_path, name):
    with pytest.raises(review.ReviewError):
        review.safe_extract(_archive({name: b"x"}), tmp_path / "out")
    assert not (tmp_path / "escape.txt").exists()


def test_safe_extract_refuses_links(tmp_path):
    with pytest.raises(review.ReviewError):
        review.safe_extract(_archive({}, links={"api/key": "/etc/shadow"}), tmp_path)


def test_tree_shows_folders_once():
    assert review.tree(["api/main.go", "api/go.mod", "frontend/src/App.jsx"]).splitlines() == [
        "api/", "  go.mod", "  main.go", "frontend/", "  src/", "    App.jsx",
    ]


class _FakeApprovals:
    def __init__(self, archive: bytes, reported_sha: str):
        self.archive = archive
        self.req = {
            "deployment_id": "dep-12345678", "application_id": "app-1", "application_name": "leave",
            "environment": "dev", "requested_by": "somchai@sti-th.com", "requested_at": "2026-10-24T09:00:00Z",
            "build_id": "b1", "images": {"api": "platform-build/leave-api:1"},
            "scans": {"api": {"passed": True, "critical": 0, "high": 1}},
            "test_report": "go test ./...: ok", "review_document": "# leave\n\n## Structure\n",
            "source_sha256": reported_sha, "source_bytes": len(archive),
            "signing_template": "company-platform deployment approval v2\n", "status": "pending_approval",
        }

    async def approval_request(self, deployment_id):
        return self.req

    async def approval_source(self, deployment_id):
        return self.archive


def test_review_lays_out_everything_for_the_approver(tmp_path, capsys):
    archive = _archive({"api/main.go": b"package main\n"})
    client = _FakeApprovals(archive, review.sha256_hex(archive))
    out = tmp_path / "rv"
    assert asyncio.run(cli._review(client, "dep-12345678", str(out))) == 0
    assert (out / "source" / "api" / "main.go").read_text() == "package main\n"
    assert (out / "REVIEW.md").read_text(encoding="utf-8").startswith("# leave")
    assert (out / "TEST_REPORT.txt").read_text(encoding="utf-8") == "go test ./...: ok"
    assert "1 high" in (out / "SCANS.txt").read_text(encoding="utf-8")
    assert (out / "SIGNING_TEMPLATE.txt").read_text(encoding="utf-8").startswith("company-platform deployment approval v2")
    assert "company-deploy approve dep-12345678" in capsys.readouterr().out


def test_review_refuses_a_source_that_doesnt_match_what_will_be_signed(tmp_path, capsys):
    archive = _archive({"api/main.go": b"package main\n"})
    client = _FakeApprovals(archive, "0" * 64)
    out = tmp_path / "rv"
    assert asyncio.run(cli._review(client, "dep-12345678", str(out))) == 1
    assert not (out / "source").exists()
    assert "don't approve" in capsys.readouterr().out
