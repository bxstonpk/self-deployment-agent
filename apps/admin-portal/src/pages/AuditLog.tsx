import { useCallback, useEffect, useState } from "react";
import { exportAuditLogCsv, queryAuditLog, verifyAuditLogIntegrity } from "../api/client";
import { ApiError } from "../api/types";
import type { AuditEntry, AuditQueryParams } from "../api/types";
import { useIdentity } from "../context/IdentityContext";
import { StatusBadge } from "../components/StatusBadge";

const RESOURCE_TYPES = ["all", "application", "deployment", "build", "audit_log"] as const;

export function AuditLog() {
  const { identity } = useIdentity();
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [resourceType, setResourceType] = useState<(typeof RESOURCE_TYPES)[number]>("all");
  const [action, setAction] = useState("");
  const [busy, setBusy] = useState<string | null>(null);

  const currentParams = useCallback((): AuditQueryParams => {
    const params: AuditQueryParams = {};
    if (resourceType !== "all") params.resourceType = resourceType;
    if (action.trim()) params.action = action.trim();
    return params;
  }, [resourceType, action]);

  const refresh = useCallback(() => {
    if (!identity) return;
    setError(null);
    queryAuditLog(identity, currentParams())
      .then((data) => setEntries(data.entries ?? []))
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)));
  }, [identity, currentParams]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function handleExport() {
    if (!identity) return;
    setBusy("export");
    setMessage(null);
    setError(null);
    try {
      const blob = await exportAuditLogCsv(identity, currentParams());
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "audit-log.csv";
      a.click();
      URL.revokeObjectURL(url);
      setMessage("Export downloaded. The export itself has been recorded as a new audit entry.");
      refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  async function handleVerify() {
    if (!identity) return;
    setBusy("verify");
    setMessage(null);
    setError(null);
    try {
      const result = await verifyAuditLogIntegrity(identity);
      setMessage(
        result.intact
          ? "Chain intact — every entry's hash still matches, and correctly chains from the one before it."
          : `Chain broken at seq ${result.broken_at_seq} — see the platform-api logs for detail.`,
      );
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  if (!identity) return null;

  return (
    <div className="page">
      <div className="page-header">
        <h1>Audit Log</h1>
      </div>
      <p className="hint">
        Shows actions you performed yourself, or that concern an application you own — there's no broader
        Auditor/Security Administrator role yet to request more than that with. See{" "}
        <code>services/platform-api/README.md</code>'s "How Audit Logging works" section for the full scope.
      </p>

      {error && <div className="error-banner">{error}</div>}
      {message && <div className="info-banner">{message}</div>}

      <div className="toolbar">
        <select
          value={resourceType}
          onChange={(e) => setResourceType(e.target.value as (typeof RESOURCE_TYPES)[number])}
          aria-label="Filter by resource type"
        >
          {RESOURCE_TYPES.map((t) => (
            <option key={t} value={t}>
              {t === "all" ? "All resource types" : t}
            </option>
          ))}
        </select>
        <input
          placeholder="Filter by action (e.g. application.suspend)"
          value={action}
          onChange={(e) => setAction(e.target.value)}
          aria-label="Filter by action"
        />
        <button onClick={refresh}>Apply filters</button>
        <button disabled={busy !== null} onClick={handleExport}>
          {busy === "export" ? "Exporting…" : "Export CSV"}
        </button>
        <button disabled={busy !== null} onClick={handleVerify}>
          {busy === "verify" ? "Verifying…" : "Verify chain integrity"}
        </button>
      </div>

      {entries === null && !error && <p>Loading…</p>}
      {entries !== null && entries.length === 0 && <p className="hint">No audit entries match.</p>}

      {entries !== null && entries.length > 0 && (
        <table className="data-table">
          <thead>
            <tr>
              <th>When</th>
              <th>Actor</th>
              <th>Action</th>
              <th>Resource</th>
              <th>Outcome</th>
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td>{new Date(e.occurred_at).toLocaleString()}</td>
                <td>{e.actor_user_id}</td>
                <td>{e.action}</td>
                <td>
                  {e.resource_type}
                  {e.resource_id ? ` / ${e.resource_id}` : ""}
                </td>
                <td>
                  <StatusBadge status={e.outcome} />
                </td>
                <td>{e.detail || <em>—</em>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
