import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useIdentity } from "../context/IdentityContext";
import {
  addDependencyRule,
  decideStandardException,
  getStandards,
  listStandardExceptions,
  removeDependencyRule,
  type Standards as StandardsData,
  type StandardException,
} from "../api/client";
import { ApiError } from "../api/types";

function message(err: unknown): string {
  return err instanceof ApiError ? err.message : String(err);
}

function ExceptionRow({ e, onDecided }: { e: StandardException; onDecided: () => void }) {
  const { identity } = useIdentity();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function decide(decision: "approve" | "reject") {
    if (!identity) return;
    if (decision === "reject" && !reason.trim()) {
      setError("Say why you're rejecting it, so the owner knows what to change.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await decideStandardException(identity, e.id, decision, reason);
      onDecided();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="exception-item">
      <div className="exception-head">
        <strong>{e.application_name}</strong>
        <span className="badge">{e.kind}</span>
        <code>{e.subject}</code>
      </div>
      <p className="exception-reason">{e.reason}</p>
      <p className="hint">
        {e.requested_by} · {new Date(e.requested_at).toLocaleString()}
      </p>
      <div className="exception-actions">
        <label>
          Reason (needed to reject)
          <input
            value={reason}
            maxLength={1000}
            onChange={(ev) => setReason(ev.target.value)}
            aria-label={`Decision reason for ${e.application_name} ${e.subject}`}
          />
        </label>
        <button type="button" className="button-primary" disabled={busy} onClick={() => decide("approve")}>
          Approve
        </button>
        <button type="button" className="button-danger" disabled={busy} onClick={() => decide("reject")}>
          Reject
        </button>
      </div>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
    </li>
  );
}

// The company's development standard, for the platform administrators:
// what it allows, the libraries it doesn't, and the exceptions waiting for
// a decision.
export function Standards() {
  const { identity } = useIdentity();
  const [standards, setStandards] = useState<StandardsData | null>(null);
  const [pending, setPending] = useState<StandardException[] | null>(null);
  const [history, setHistory] = useState<StandardException[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [rule, setRule] = useState({ ecosystem: "npm", package: "", reason: "" });
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    if (!identity) return;
    getStandards(identity).then(setStandards).catch((err) => setError(message(err)));
    listStandardExceptions(identity, "pending")
      .then((d) => setPending(d.exceptions))
      .catch((err) => setError(message(err)));
    Promise.all([listStandardExceptions(identity, "approved"), listStandardExceptions(identity, "rejected")])
      .then(([a, r]) =>
        setHistory([...a.exceptions, ...r.exceptions].sort((x, y) => (y.decided_at ?? "").localeCompare(x.decided_at ?? ""))),
      )
      .catch(() => setHistory([]));
  }, [identity]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleAdd(e: FormEvent) {
    e.preventDefault();
    if (!identity) return;
    setSaving(true);
    setError(null);
    try {
      await addDependencyRule(identity, rule);
      setRule({ ...rule, package: "", reason: "" });
      load();
    } catch (err) {
      setError(message(err));
    } finally {
      setSaving(false);
    }
  }

  async function handleRemove(id: string) {
    if (!identity) return;
    setError(null);
    try {
      await removeDependencyRule(identity, id);
      load();
    } catch (err) {
      setError(message(err));
    }
  }

  const allowed = (kind: string) =>
    (standards?.stacks ?? []).filter((s) => s.kind === kind && s.status === "active").map((s) => s.name);

  return (
    <div className="page">
      <div className="page-header">
        <h1>Standards</h1>
      </div>
      <p className="hint">
        The company standard applies from the moment an application is started: Claude reads it with get_standards,
        registers every new application with its profile, and asks here before using anything outside it.
      </p>

      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}

      <section className="card">
        <h2>Exceptions waiting for you{pending && pending.length > 0 ? ` (${pending.length})` : ""}</h2>
        {pending === null ? (
          <p className="hint">Loading…</p>
        ) : pending.length === 0 ? (
          <p className="empty-state">Nothing waiting.</p>
        ) : (
          <ul className="exception-list">
            {pending.map((e) => (
              <ExceptionRow key={e.id} e={e} onDecided={load} />
            ))}
          </ul>
        )}
      </section>

      <section className="card">
        <h2>Libraries the company doesn't allow</h2>
        <p className="hint">
          A build that uses one fails, unless the application has an approved exception for it. Checked in each
          service's package.json, go.mod and requirements.txt.
        </p>
        <form onSubmit={handleAdd} className="department-form">
          <label>
            Ecosystem
            <select value={rule.ecosystem} onChange={(e) => setRule({ ...rule, ecosystem: e.target.value })}>
              <option value="npm">npm</option>
              <option value="go">go</option>
              <option value="pypi">pypi</option>
            </select>
          </label>
          <label>
            Package
            <input
              required
              maxLength={214}
              value={rule.package}
              onChange={(e) => setRule({ ...rule, package: e.target.value })}
              placeholder="e.g. left-pad"
            />
          </label>
          <label>
            Why
            <input
              required
              maxLength={1000}
              value={rule.reason}
              onChange={(e) => setRule({ ...rule, reason: e.target.value })}
              placeholder="e.g. unmaintained; use …"
            />
          </label>
          <button type="submit" className="button-primary" disabled={saving || !rule.package.trim() || !rule.reason.trim()}>
            {saving ? "Adding…" : "Add rule"}
          </button>
        </form>
        {standards && standards.dependency_rules.length === 0 ? (
          <p className="empty-state">No rules yet: every library is allowed.</p>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Ecosystem</th>
                  <th>Package</th>
                  <th>Why</th>
                  <th>Added by</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {(standards?.dependency_rules ?? []).map((r) => (
                  <tr key={r.id}>
                    <td>{r.ecosystem}</td>
                    <td>
                      <code>{r.package}</code>
                    </td>
                    <td>{r.reason}</td>
                    <td>{r.created_by}</td>
                    <td>
                      <button type="button" onClick={() => handleRemove(r.id)}>
                        Remove
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="card">
        <h2>The standard</h2>
        {standards && (
          <>
            <p>
              Frontend: {allowed("frontend").join(", ") || "—"} · API: {allowed("backend").join(", ") || "—"} · Database:{" "}
              {allowed("database").join(", ") || "—"}
            </p>
            <ul className="standard-rules">
              {standards.rules.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
            <p className="hint">
              {standards.profile_required_to_build
                ? "A build refuses an application without a profile."
                : "Profiles are not required to build (GOVERNANCE_REQUIRE_PROFILE=false)."}
            </p>
          </>
        )}
      </section>

      {history.length > 0 && (
        <section className="card">
          <h2>Decided exceptions</h2>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Application</th>
                  <th>Exception</th>
                  <th>Decision</th>
                  <th>By</th>
                  <th>Reason</th>
                </tr>
              </thead>
              <tbody>
                {history.map((e) => (
                  <tr key={e.id}>
                    <td>{e.application_name}</td>
                    <td>
                      {e.kind} <code>{e.subject}</code>
                    </td>
                    <td>{e.status}</td>
                    <td>{e.decided_by}</td>
                    <td>{e.decision_reason}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}
