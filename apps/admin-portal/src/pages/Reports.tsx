import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { applicationInventory, deploymentActivity } from "../api/client";
import { ApiError } from "../api/types";
import type { DeploymentActivityReport, InventoryRow, OutcomeCounts } from "../api/types";
import { useIdentity } from "../context/IdentityContext";
import { StatusBadge } from "../components/StatusBadge";

// The API's own default window when from/to are omitted — mirrored here
// only to pre-fill the date inputs so the form shows what you're actually
// looking at, rather than two empty boxes.
function defaultRange(): { from: string; to: string } {
  const to = new Date();
  const from = new Date(to);
  from.setDate(from.getDate() - 30);
  const asDateInput = (d: Date) => d.toISOString().slice(0, 10);
  return { from: asDateInput(from), to: asDateInput(to) };
}

function OutcomeCells({ counts }: { counts: OutcomeCounts }) {
  return (
    <>
      <td className="numeric">{counts.succeeded}</td>
      <td className="numeric">{counts.failed}</td>
      <td className="numeric">{counts.rolled_back}</td>
    </>
  );
}

function BreakdownTable({ title, rows }: { title: string; rows: Record<string, OutcomeCounts> }) {
  const entries = Object.entries(rows);
  if (entries.length === 0) return <p className="hint">No {title.toLowerCase()} activity in this range.</p>;
  return (
    <table className="data-table breakdown-table" aria-label={`${title} breakdown`}>
      <thead>
        <tr>
          <th>{title}</th>
          <th className="numeric">Succeeded</th>
          <th className="numeric">Failed</th>
          <th className="numeric">Rolled back</th>
        </tr>
      </thead>
      <tbody>
        {entries.map(([key, counts]) => (
          <tr key={key}>
            <td>{key}</td>
            <OutcomeCells counts={counts} />
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function Reports() {
  const { identity } = useIdentity();
  const [inventory, setInventory] = useState<InventoryRow[] | null>(null);
  const [activity, setActivity] = useState<DeploymentActivityReport | null>(null);
  const [range, setRange] = useState(defaultRange);
  const [rangeHint, setRangeHint] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadInventory = useCallback(() => {
    if (!identity) return;
    applicationInventory(identity)
      .then((r) => setInventory(r.applications ?? []))
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)));
  }, [identity]);

  const loadActivity = useCallback(() => {
    if (!identity) return;
    setError(null);
    // A half-edited range is normal, not exceptional: clearing a date
    // input to retype it leaves it empty for a keystroke or two, and
    // editing the pair in sequence necessarily passes through `from` being
    // later than `to`. Neither should reach the API — an empty value
    // parses to an Invalid Date (whose toISOString() throws outright), and
    // a backwards range earns a real 400 that flashes a red banner at
    // someone who is simply mid-edit. Same "don't invite a confusing
    // error" reasoning as this app's button-enablement rules; the API
    // still validates the range itself regardless of what this shows.
    // The empty-input crash was a real one, found by driving the page.
    const from = new Date(`${range.from}T00:00:00`);
    const to = new Date(`${range.to}T23:59:59`);
    if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) {
      setRangeHint("Pick both a start and an end date to see the report.");
      return;
    }
    if (from > to) {
      setRangeHint("Start date is after the end date — adjust either one to see the report.");
      return;
    }
    setRangeHint(null);
    deploymentActivity(identity, from.toISOString(), to.toISOString())
      .then(setActivity)
      .catch((err) => setError(err instanceof ApiError ? `${err.code}: ${err.message}` : String(err)));
  }, [identity, range]);

  useEffect(() => {
    loadInventory();
  }, [loadInventory]);

  useEffect(() => {
    loadActivity();
  }, [loadActivity]);

  if (!identity) return null;

  return (
    <div className="page">
      <div className="page-header">
        <h1>Reports</h1>
      </div>
      <p className="hint">
        Covers applications you own — there's no platform-wide reporting role yet, so this is the owner-scoped view.
        See <code>services/platform-api/README.md</code>'s "How Reporting works" section for the full scope.
      </p>

      {error && <div className="error-banner">{error}</div>}

      <section className="card">
        <h2>Deployment activity</h2>
        <div className="toolbar">
          <label>
            From{" "}
            <input
              type="date"
              value={range.from}
              onChange={(e) => setRange((r) => ({ ...r, from: e.target.value }))}
              aria-label="Report range start date"
            />
          </label>
          <label>
            To{" "}
            <input
              type="date"
              value={range.to}
              onChange={(e) => setRange((r) => ({ ...r, to: e.target.value }))}
              aria-label="Report range end date"
            />
          </label>
        </div>

        {rangeHint && <p className="hint">{rangeHint}</p>}
        {activity === null && !error && !rangeHint && <p>Loading…</p>}
        {activity && (
          <>
            {activity.available_from && (
              <p className="hint">
                Data only goes back to {new Date(activity.available_from).toLocaleString()} — the range you asked for
                starts earlier than anything recorded.
              </p>
            )}
            <div className="stat-row" role="group" aria-label="Deployment outcome totals">
              <div className="stat-tile stat-succeeded">
                <span className="stat-label">Succeeded</span>
                <span className="stat-value">{activity.total.succeeded}</span>
              </div>
              <div className="stat-tile stat-failed">
                <span className="stat-label">Failed</span>
                <span className="stat-value">{activity.total.failed}</span>
              </div>
              <div className="stat-tile stat-rolled-back">
                <span className="stat-label">Rolled back</span>
                <span className="stat-value">{activity.total.rolled_back}</span>
              </div>
            </div>
            <p className="hint">
              A rollback creates a real deployment of its own, counted here rather than as a plain success — see the
              platform-wide <Link to="/audit-log">Audit Log</Link> for the individual actions these totals come from.
            </p>
            <BreakdownTable title="Environment" rows={activity.by_environment} />
            <BreakdownTable title="Department" rows={activity.by_department} />
          </>
        )}
      </section>

      <section className="card">
        <h2>Application inventory</h2>
        {inventory === null && !error && <p>Loading…</p>}
        {inventory !== null && inventory.length === 0 && <p className="hint">You don't own any applications yet.</p>}
        {inventory !== null && inventory.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Department</th>
                <th>Status</th>
                <th>Stack</th>
                <th>Environment</th>
                <th className="numeric">Owners</th>
              </tr>
            </thead>
            <tbody>
              {inventory.map((row) => (
                <tr key={row.application_id}>
                  <td>
                    <Link to={`/applications/${row.application_id}`}>{row.name}</Link>
                  </td>
                  <td>{row.department_name || row.department_id}</td>
                  <td>
                    <StatusBadge status={row.lifecycle_status} />
                  </td>
                  <td>{row.runtimes.length > 0 ? row.runtimes.join(", ") : <em>—</em>}</td>
                  <td>{row.environment || <em>never deployed</em>}</td>
                  <td className="numeric">{row.owner_user_ids.length}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
