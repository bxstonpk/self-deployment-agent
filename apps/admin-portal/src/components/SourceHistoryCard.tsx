import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useIdentity } from "../context/IdentityContext";
import { getBuildSource, listSources, type SourceBuild } from "../api/client";
import { ApiError } from "../api/types";
import { readTarGz, type SourceFile } from "../approval/tarball";
import { StatusBadge } from "./StatusBadge";

// The viewer (with its syntax highlighting) loads only when a build is opened.
const SourceFileBrowser = lazy(() => import("./SourceFileBrowser").then((m) => ({ default: m.SourceFileBrowser })));

type Show = "all" | "production" | "dev";

function kb(bytes: number): string {
  return `${Math.max(1, Math.round(bytes / 1024)).toLocaleString()} KB`;
}

// Every build of the application and of its dev copy, with the source each
// was built from, readable at any time (not only while waiting for
// approval). Shown to the application's owners, administrators and
// approvers; hidden from anyone else.
export function SourceHistoryCard({ applicationId }: { applicationId: string }) {
  const { identity } = useIdentity();
  const [builds, setBuilds] = useState<SourceBuild[] | null>(null);
  const [hidden, setHidden] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [show, setShow] = useState<Show>("all");
  const [open, setOpen] = useState<SourceBuild | null>(null);
  const [files, setFiles] = useState<SourceFile[] | null>(null);
  const [archive, setArchive] = useState<Uint8Array | null>(null);
  const [fileState, setFileState] = useState<"loading" | "ready" | "error">("loading");

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    listSources(identity, applicationId)
      .then((r) => !cancelled && setBuilds(r.builds))
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError && (err.status === 403 || err.status === 404)) setHidden(true);
        else setError(err instanceof ApiError ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [identity, applicationId]);

  // Each opening is its own download; an answer to an earlier one (another
  // build, or the same one opened again) is dropped. Opening the same build
  // twice used to clear its files and never fetch them again, leaving
  // "Loading the source…" for good (found on the LAN server, 2026-11-06).
  const request = useRef(0);
  const viewer = useRef<HTMLDivElement>(null);

  async function openBuild(b: SourceBuild) {
    if (!identity) return;
    const n = ++request.current;
    setOpen(b);
    setFiles(null);
    setArchive(null);
    setFileState("loading");
    requestAnimationFrame(() => viewer.current?.scrollIntoView?.({ behavior: "smooth", block: "start" }));
    try {
      const bytes = await getBuildSource(identity, applicationId, b.build_id);
      if (n !== request.current) return;
      const read = readTarGz(bytes);
      setArchive(bytes);
      setFiles(read);
      setFileState("ready");
    } catch {
      if (n === request.current) setFileState("error");
    }
  }

  function closeBuild() {
    request.current++;
    setOpen(null);
  }

  if (hidden) return null;

  const shown = (builds ?? []).filter((b) => show === "all" || (show === "dev") === b.dev_copy);
  const hasDev = (builds ?? []).some((b) => b.dev_copy);

  return (
    <section className="card">
      <h2>Source</h2>
      <p className="hint">
        The files each build was made from, exactly as they were uploaded — {hasDev ? "this application's and its dev copy's" : "every build"}, kept until the
        application is deleted.
      </p>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {!error && builds === null && <p className="hint">Loading…</p>}
      {builds && builds.length === 0 && <p className="empty-state">Nothing has been built yet.</p>}
      {builds && builds.length > 0 && (
        <>
          {hasDev && (
            <div className="tabs" role="tablist" aria-label="Which builds">
              {(["all", "production", "dev"] as Show[]).map((s) => (
                <button
                  key={s}
                  type="button"
                  role="tab"
                  aria-selected={show === s}
                  className={show === s ? "tab active" : "tab"}
                  onClick={() => setShow(s)}
                >
                  {s === "all" ? "All" : s === "production" ? "Application" : "Dev copy"}
                </button>
              ))}
            </div>
          )}
          <div className="table-wrap source-history-table">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Built</th>
                  <th>Application</th>
                  <th>By</th>
                  <th>Build</th>
                  <th>Deployed</th>
                  <th className="numeric">Size</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {shown.map((b) => (
                  <tr key={b.build_id} className={open?.build_id === b.build_id ? "selected" : undefined}>
                    <td>{new Date(b.started_at).toLocaleString()}</td>
                    <td>
                      {b.application_name}
                      {b.dev_copy && <span className="badge badge-info"> dev</span>}
                    </td>
                    <td>{b.triggered_by}</td>
                    <td>
                      <StatusBadge status={b.status} />
                    </td>
                    <td>
                      {b.deployments.length === 0
                        ? "—"
                        : b.deployments.map((d) => (
                            <div key={d.id}>
                              {d.environment}{" "}
                              <StatusBadge status={d.status} />
                            </div>
                          ))}
                    </td>
                    <td className="numeric">{b.kept ? kb(b.source_bytes) : "—"}</td>
                    <td>
                      {b.kept ? (
                        <button type="button" onClick={() => openBuild(b)} aria-label={`View the source of ${b.application_name} built ${new Date(b.started_at).toLocaleString()}`}>
                          View files
                        </button>
                      ) : (
                        <span className="hint">Not kept</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      {open && (
        <div className="source-viewer" ref={viewer}>
          <h3>
            {open.application_name} · {new Date(open.started_at).toLocaleString()} · <code>{open.build_id.slice(0, 8)}</code>{" "}
            <button type="button" onClick={closeBuild}>
              Close
            </button>
          </h3>
          {fileState === "error" ? (
            <div className="error-banner" role="alert">
              Couldn't download this build's source.
            </div>
          ) : (
            <Suspense fallback={<p className="hint">Loading the source…</p>}>
              <SourceFileBrowser
                files={files}
                state={fileState}
                archive={archive}
                name={`${open.application_name}-${open.build_id.slice(0, 8)}`}
              />
            </Suspense>
          )}
        </div>
      )}
    </section>
  );
}
