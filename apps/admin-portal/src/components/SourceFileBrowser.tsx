import { useMemo, useState } from "react";
import { asText, type SourceFile } from "../approval/tarball";

// A build's source, file by file, as it was uploaded: used by the approval
// page and by an application's source history.
export function SourceFileBrowser({
  files,
  state,
  archive,
  name,
}: {
  files: SourceFile[] | null;
  state: string;
  archive: Uint8Array | null;
  name: string;
}) {
  const [selected, setSelected] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const shown = useMemo(
    () => (files ?? []).filter((f) => !filter || f.path.toLowerCase().includes(filter.toLowerCase())),
    [files, filter],
  );
  const current = files?.find((f) => f.path === (selected ?? files[0]?.path)) ?? null;

  if (state === "mismatch") {
    return (
      <div className="error-banner" role="alert">
        The downloaded source doesn't match the SHA-256 the platform reported. Don't approve; check the platform.
      </div>
    );
  }
  if (!files) return <p className="hint">{state === "none" ? "No source was kept for this build." : "Loading the source…"}</p>;

  function download() {
    if (!archive) return;
    const url = URL.createObjectURL(new Blob([archive.slice().buffer], { type: "application/gzip" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `${name}-source.tar.gz`;
    a.click();
    URL.revokeObjectURL(url);
  }

  const text = current ? asText(current.data) : null;
  return (
    <div className="file-browser">
      <div className="file-list">
        <input
          type="search"
          aria-label="Filter files"
          placeholder="Filter files…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <ul>
          {shown.map((f) => (
            <li key={f.path}>
              <button
                type="button"
                className={current?.path === f.path ? "file active" : "file"}
                onClick={() => setSelected(f.path)}
                title={f.path}
              >
                {f.path}
              </button>
            </li>
          ))}
        </ul>
        <button type="button" onClick={download}>
          Download all (.tar.gz)
        </button>
      </div>
      <div className="file-view">
        {current && (
          <>
            <div className="file-view-head">
              <code>{current.path}</code>
              <span>{current.data.length.toLocaleString()} bytes</span>
            </div>
            {text === null ? (
              <p className="hint">Binary file — not shown.</p>
            ) : (
              <pre className="code-view" aria-label={`Contents of ${current.path}`}>
                {text.split("\n").map((line, i) => (
                  <span key={i} className="code-line">
                    <span className="ln">{i + 1}</span>
                    {line + "\n"}
                  </span>
                ))}
              </pre>
            )}
          </>
        )}
      </div>
    </div>
  );
}
