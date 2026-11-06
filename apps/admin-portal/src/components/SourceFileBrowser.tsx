import { useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { asText, type SourceFile } from "../approval/tarball";
import { highlightLines } from "../approval/highlight";

// A build's source, file by file, as it was uploaded — laid out like a code
// host: a folder tree, the file with line numbers and highlighting, Markdown
// and images previewed. Used by the approval page and an application's
// source history.

interface Dir {
  name: string;
  path: string;
  dirs: Dir[];
  files: SourceFile[];
}

function buildTree(files: SourceFile[]): Dir {
  const root: Dir = { name: "", path: "", dirs: [], files: [] };
  for (const f of files) {
    const parts = f.path.split("/");
    let dir = root;
    for (const part of parts.slice(0, -1)) {
      const path = dir.path ? `${dir.path}/${part}` : part;
      let next = dir.dirs.find((d) => d.name === part);
      if (!next) {
        next = { name: part, path, dirs: [], files: [] };
        dir.dirs.push(next);
      }
      dir = next;
    }
    dir.files.push(f);
  }
  const sort = (d: Dir) => {
    d.dirs.sort((a, b) => a.name.localeCompare(b.name));
    d.files.sort((a, b) => a.path.localeCompare(b.path));
    d.dirs.forEach(sort);
  };
  sort(root);
  return root;
}

function baseName(path: string): string {
  return path.split("/").pop() ?? path;
}

function ancestors(path: string): string[] {
  const parts = path.split("/").slice(0, -1);
  return parts.map((_, i) => parts.slice(0, i + 1).join("/"));
}

function sizeText(bytes: number): string {
  if (bytes < 1024) return `${bytes} Bytes`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

const IMAGE_TYPES: Record<string, string> = {
  png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp",
  svg: "image/svg+xml", ico: "image/x-icon",
};

function imageType(path: string): string | null {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  return IMAGE_TYPES[ext] ?? null;
}

function isMarkdown(path: string): boolean {
  return /\.(md|markdown)$/i.test(path);
}

// What to open first: a README or REVIEW.md nearest the top, else the first file.
function firstFile(files: SourceFile[]): SourceFile | null {
  const docs = files
    .filter((f) => /^(readme|review)(\.md)?$/i.test(baseName(f.path)))
    .sort((a, b) => a.path.split("/").length - b.path.split("/").length);
  return docs[0] ?? files[0] ?? null;
}

function saveBlob(data: BlobPart, name: string, type: string) {
  const url = URL.createObjectURL(new Blob([data], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

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
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [preview, setPreview] = useState(true);
  const [line, setLine] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);

  const tree = useMemo(() => buildTree(files ?? []), [files]);
  const current = useMemo(() => {
    if (!files) return null;
    return files.find((f) => f.path === selected) ?? firstFile(files);
  }, [files, selected]);
  const matches = useMemo(
    () => (filter ? (files ?? []).filter((f) => f.path.toLowerCase().includes(filter.toLowerCase())) : []),
    [files, filter],
  );

  if (state === "mismatch") {
    return (
      <div className="error-banner" role="alert">
        The downloaded source doesn't match the SHA-256 the platform reported. Don't approve; check the platform.
      </div>
    );
  }
  if (!files) return <p className="hint">{state === "none" ? "No source was kept for this build." : "Loading the source…"}</p>;
  if (files.length === 0) return <p className="empty-state">The archive has no files.</p>;

  function open(path: string) {
    setSelected(path);
    setLine(null);
    setPreview(true);
    setCopied(false);
    // The folders above it are shown.
    setCollapsed((c) => {
      const next = new Set(c);
      ancestors(path).forEach((a) => next.delete(a));
      return next;
    });
  }

  function toggle(path: string) {
    setCollapsed((c) => {
      const next = new Set(c);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }

  function renderDir(dir: Dir, depth: number) {
    return (
      <>
        {dir.dirs.map((d) => {
          const isOpen = !collapsed.has(d.path);
          return (
            <li key={`d:${d.path}`}>
              <button
                type="button"
                className="tree-row tree-dir"
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
                onClick={() => toggle(d.path)}
                aria-expanded={isOpen}
              >
                <span className="tree-caret" aria-hidden="true">{isOpen ? "▾" : "▸"}</span>
                <span className="tree-icon" aria-hidden="true">📁</span>
                {d.name}
              </button>
              {isOpen && <ul>{renderDir(d, depth + 1)}</ul>}
            </li>
          );
        })}
        {dir.files.map((f) => (
          <li key={`f:${f.path}`}>
            <button
              type="button"
              className={current?.path === f.path ? "tree-row tree-file active" : "tree-row tree-file"}
              style={{ paddingLeft: `${depth * 14 + 22}px` }}
              onClick={() => open(f.path)}
              title={f.path}
              aria-label={f.path}
              aria-current={current?.path === f.path ? "true" : undefined}
            >
              <span className="tree-icon" aria-hidden="true">📄</span>
              {baseName(f.path)}
            </button>
          </li>
        ))}
      </>
    );
  }

  return (
    <div className="file-browser">
      <nav className="file-list" aria-label="Files">
        <input
          type="search"
          aria-label="Filter files"
          placeholder="Go to file…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <ul className="tree">
          {filter ? (
            matches.length === 0 ? (
              <li className="hint">No file matches.</li>
            ) : (
              matches.map((f) => (
                <li key={f.path}>
                  <button
                    type="button"
                    className={current?.path === f.path ? "tree-row tree-file active" : "tree-row tree-file"}
                    onClick={() => open(f.path)}
                    title={f.path}
                  >
                    {f.path}
                  </button>
                </li>
              ))
            )
          ) : (
            renderDir(tree, 0)
          )}
        </ul>
        <p className="hint">{files.length} files</p>
        <button type="button" onClick={() => archive && saveBlob(archive.slice().buffer, `${name}-source.tar.gz`, "application/gzip")}>
          Download all (.tar.gz)
        </button>
      </nav>
      {current && (
        <FileView
          key={current.path}
          file={current}
          name={name}
          preview={preview}
          setPreview={setPreview}
          line={line}
          setLine={setLine}
          copied={copied}
          setCopied={setCopied}
          onCrumb={(dirPath) =>
            setCollapsed((c) => {
              const next = new Set(c);
              [...ancestors(`${dirPath}/x`)].forEach((a) => next.delete(a));
              return next;
            })
          }
        />
      )}
    </div>
  );
}

function FileView({
  file,
  name,
  preview,
  setPreview,
  line,
  setLine,
  copied,
  setCopied,
  onCrumb,
}: {
  file: SourceFile;
  name: string;
  preview: boolean;
  setPreview: (v: boolean) => void;
  line: number | null;
  setLine: (n: number | null) => void;
  copied: boolean;
  setCopied: (v: boolean) => void;
  onCrumb: (dirPath: string) => void;
}) {
  const text = useMemo(() => asText(file.data), [file]);
  const lines = useMemo(() => (text === null ? [] : highlightLines(text, file.path)), [text, file.path]);
  const image = imageType(file.path);
  const imageUrl = useMemo(
    () => (image ? URL.createObjectURL(new Blob([file.data.slice().buffer], { type: image })) : null),
    [file, image],
  );
  useEffect(() => () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
  }, [imageUrl]);

  const markdown = text !== null && isMarkdown(file.path);
  const parts = file.path.split("/");

  async function copy() {
    if (text === null) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }

  return (
    <section className="file-view" aria-label={`File ${file.path}`}>
      <div className="file-crumbs">
        <span className="crumb-root">{name}</span>
        {parts.map((p, i) => (
          <span key={i}>
            <span className="crumb-sep">/</span>
            {i === parts.length - 1 ? (
              <strong>{p}</strong>
            ) : (
              <button type="button" className="crumb" onClick={() => onCrumb(parts.slice(0, i + 1).join("/"))}>
                {p}
              </button>
            )}
          </span>
        ))}
      </div>
      <div className="file-view-head">
        <div className="file-view-meta">
          {markdown && (
            <span className="segmented" role="group" aria-label="Show as">
              <button type="button" className={preview ? "active" : ""} aria-pressed={preview} onClick={() => setPreview(true)}>
                Preview
              </button>
              <button type="button" className={!preview ? "active" : ""} aria-pressed={!preview} onClick={() => setPreview(false)}>
                Code
              </button>
            </span>
          )}
          <span>
            {text !== null && `${lines.length.toLocaleString()} lines · `}
            {sizeText(file.data.length)}
          </span>
        </div>
        <div className="file-view-actions">
          {text !== null && (
            <button type="button" onClick={copy}>
              {copied ? "Copied" : "Copy"}
            </button>
          )}
          <button type="button" onClick={() => saveBlob(file.data.slice().buffer, baseName(file.path), "application/octet-stream")}>
            Download
          </button>
        </div>
      </div>
      {image && imageUrl ? (
        <div className="file-image">
          <img src={imageUrl} alt={file.path} />
        </div>
      ) : text === null ? (
        <p className="hint file-binary">Binary file — not shown.</p>
      ) : markdown && preview ? (
        <div className="markdown file-markdown">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
        </div>
      ) : (
        <div className="code-view" role="region" aria-label={`Contents of ${file.path}`}>
          <table className="code-table">
            <tbody>
              {lines.map((html, i) => (
                <tr key={i} className={line === i + 1 ? "code-row selected" : "code-row"}>
                  <td className="ln">
                    <button type="button" aria-label={`Line ${i + 1}`} onClick={() => setLine(line === i + 1 ? null : i + 1)}>
                      {i + 1}
                    </button>
                  </td>
                  {/* highlight.js output: escaped text and its own spans only */}
                  <td className="code-line hljs" dangerouslySetInnerHTML={{ __html: html || " " }} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
