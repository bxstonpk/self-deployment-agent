import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { downloadHandoff, listHandoffs, rebuildHandoff, type HandoffEntry, type HandoffPackage } from "../api/client";
import { ApiError } from "../api/types";
import { formatBytes } from "../format";

// IT handoff (DEC-033): this platform is the development server, and IT runs
// the applications for real. Every application promoted to production here
// is packed as one .tar — its images, its database, its source and the
// files to run it with Docker — for an administrator or approver to download
// and give to IT.
const POLL_MS = 10_000;

export function Handoffs() {
  const { identity } = useIdentity();
  const [entries, setEntries] = useState<HandoffEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!identity) return;
    listHandoffs(identity)
      .then((d) => {
        setEntries(d.handoffs);
        setError(null);
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 403)
          setError("Only administrators and approvers can hand applications to IT.");
        else setError(err instanceof Error ? err.message : String(err));
      });
  }, [identity]);

  useEffect(() => {
    load();
  }, [load]);

  // While a package is being made, keep the page current.
  const building = entries?.some((e) => e.latest?.status === "building") ?? false;
  useEffect(() => {
    if (!building) return;
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [building, load]);

  async function download(p: HandoffPackage) {
    if (!identity) return;
    setBusy(p.id);
    setMessage(null);
    setError(null);
    try {
      const blob = await downloadHandoff(identity, p.id);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = p.file_name ?? "handoff.tar";
      a.click();
      URL.revokeObjectURL(url);
      setMessage(`${p.file_name} downloaded. The download is recorded in the audit log.`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  async function rebuild(e: HandoffEntry) {
    if (!identity) return;
    setBusy(e.application_id);
    setMessage(null);
    setError(null);
    try {
      await rebuildHandoff(identity, e.application_id);
      setMessage(`Making ${e.application_name}'s package again, of what runs in production now.`);
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <h1>IT handoff</h1>
      </div>
      <p className="hint">
        Every application promoted to production is packed for IT as one <code>.tar</code>: its images, its database,
        its source, and a README with how to run it. Download it and give it to IT. The settings' values (passwords,
        webhooks) are not in it, only their names: IT asks the owners for them.
      </p>
      {message && (
        <div className="info-banner" role="status">
          {message}
        </div>
      )}
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {!error && entries === null && <p className="hint">Loading…</p>}
      {entries && entries.length === 0 && (
        <p className="empty-state">No application has been promoted to production yet.</p>
      )}
      {entries && entries.length > 0 && (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Application</th>
                <th>Package</th>
                <th>Made</th>
                <th>Size</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.application_id}>
                  <td>
                    <Link to={`/applications/${e.application_id}`}>{e.application_name || e.application_id}</Link>
                  </td>
                  <td>
                    <PackageStatus entry={e} />
                  </td>
                  <td>{e.ready?.completed_at ? new Date(e.ready.completed_at).toLocaleString() : "—"}</td>
                  <td>{e.ready?.size_bytes ? formatBytes(e.ready.size_bytes) : "—"}</td>
                  <td>
                    {e.ready && (
                      <button
                        type="button"
                        className="button-primary"
                        disabled={busy !== null}
                        onClick={() => download(e.ready!)}
                      >
                        {busy === e.ready.id ? "Downloading…" : "Download .tar"}
                      </button>
                    )}{" "}
                    <button
                      type="button"
                      disabled={busy !== null || e.latest?.status === "building"}
                      onClick={() => rebuild(e)}
                    >
                      Make again
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function PackageStatus({ entry }: { entry: HandoffEntry }) {
  const { ready, latest } = entry;
  return (
    <>
      {ready && (
        <div>
          <span className="badge badge-success">ready</span>{" "}
          <code title={`SHA-256 ${ready.sha256}`}>{ready.file_name}</code>
        </div>
      )}
      {latest?.status === "building" && (
        <div>
          <span className="badge badge-info">being made</span>{" "}
          {ready ? "a newer one, of what runs now" : "of what runs in production now"}
        </div>
      )}
      {latest?.status === "failed" && (
        <div>
          <span className="badge badge-danger">failed</span> {latest.error}
        </div>
      )}
      {ready && (
        <div className="hint">
          {ready.trigger === "promotion" ? "Made when it was promoted" : "Made again on request"} · deployment{" "}
          <code>{ready.deployment_id.slice(0, 8)}</code>
        </div>
      )}
    </>
  );
}
