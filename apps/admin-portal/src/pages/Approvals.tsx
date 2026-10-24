import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { listPendingApprovals, type ApprovalRequest } from "../api/client";
import { ApiError } from "../api/types";

// Deployments waiting for the approver's signature. Each opens to everything
// there is to review — REVIEW.md, every source file, the tests and scans —
// and the place to sign.
export function Approvals() {
  const { identity } = useIdentity();
  const [pending, setPending] = useState<ApprovalRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!identity) return;
    listPendingApprovals(identity)
      .then((d) => setPending(d.pending))
      .catch((err) => {
        if (err instanceof ApiError && err.status === 403) setError("Only an approver (someone holding an approver key) can see this page.");
        else if (err instanceof ApiError && err.status === 404) setError("Signed approvals aren't set up on this platform (no PLATFORM_APPROVER_KEYS).");
        else setError(err instanceof Error ? err.message : String(err));
      });
  }, [identity]);

  return (
    <div className="page">
      <div className="page-header">
        <h1>Approvals</h1>
      </div>
      <p className="hint">
        Deployments waiting for your signature. Open one to read its REVIEW.md and every file that was built, then
        approve or reject it with your approver key.
      </p>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {!error && pending === null && <p className="hint">Loading…</p>}
      {pending && pending.length === 0 && <p className="empty-state">Nothing is waiting for approval.</p>}
      {pending && pending.length > 0 && (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Application</th>
                <th>Environment</th>
                <th>Requested by</th>
                <th>Requested</th>
                <th>Files</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {pending.map((p) => (
                <tr key={p.deployment_id}>
                  <td>{p.application_name}</td>
                  <td>
                    <span className={`env-badge env-${p.environment}`}>{p.environment}</span>
                  </td>
                  <td>{p.requested_by}</td>
                  <td>{new Date(p.requested_at).toLocaleString()}</td>
                  <td>{p.source_sha256 ? `${Math.max(1, Math.round(p.source_bytes / 1024))} KB` : "—"}</td>
                  <td>
                    <Link to={`/approvals/${p.deployment_id}`} className="button-link">
                      Review
                    </Link>
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
