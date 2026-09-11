import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import {
  acceptOwnershipTransfer,
  archiveApplication,
  deleteApplication,
  deleteSecret,
  deployApplication,
  deploymentHistory,
  getApplication,
  getLogs,
  getMetrics,
  getPendingOwnershipTransfer,
  grantOwner,
  initiateOwnershipTransfer,
  latestBuild,
  latestDeployment,
  listOwners,
  listScaleEvents,
  listSecrets,
  queryAuditLog,
  restartApplication,
  resumeApplication,
  revokeOwner,
  rollbackApplication,
  rotateSecret,
  saveDeploymentYaml,
  setSecret,
  suspendApplication,
  triggerBuild,
  validateApplication,
} from "../api/client";
import { ApiError } from "../api/types";
import type {
  Application,
  ApplicationOwner,
  AuditEntry,
  Build,
  Deployment,
  DeploymentStatus,
  LogEntry,
  MetricsSnapshot,
  OwnershipRole,
  OwnershipTransfer,
  ScaleEvent,
  SecretMetadata,
  ValidationReport,
} from "../api/types";
import { useIdentity } from "../context/IdentityContext";
import { StatusBadge } from "../components/StatusBadge";

// Mirrors lifecycle_service.go's requireNothingLive: deployment statuses that
// still count as "in progress".
const IN_FLIGHT_DEPLOYMENT: DeploymentStatus[] = ["scanning", "pending_approval", "deploying", "health_check"];

// One screenful of log lines at a time; older ones come from the cursor the
// platform hands back, never from a client-side offset.
const LOG_PAGE = 50;

// The windows the Metrics section offers, in minutes. The platform
// defaults to the last hour and caps any window at seven days.
const METRICS_RANGES = { "15m": 15, "1h": 60, "24h": 24 * 60 } as const;
type MetricsRange = keyof typeof METRICS_RANGES;

function formatBytes(bytes: number): string {
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${mb.toFixed(mb < 10 ? 1 : 0)} MB`;
}

// Oldest on the left. Drawn straight from the readings, with nothing
// interpolated across a gap where the platform took none.
function Sparkline({ values, label }: { values: number[]; label: string }) {
  if (values.length < 2) return null;
  const width = 240;
  const height = 40;
  const ceiling = Math.max(...values, 1);
  const points = values
    .map((v, i) => `${(i / (values.length - 1)) * width},${height - (v / ceiling) * height}`)
    .join(" ");
  return (
    <svg className="sparkline" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={label}>
      <polyline points={points} />
    </svg>
  );
}

// Requests per minute, with the failed ones drawn over them in the danger
// colour — and stated in the caption, so the colour isn't carrying it alone.
function Bars({ series, label }: { series: { total: number; errors: number }[]; label: string }) {
  if (series.length === 0) return null;
  const width = 240;
  const height = 40;
  const ceiling = Math.max(...series.map((s) => s.total), 1);
  // Capped, so one minute of traffic draws one bar rather than a
  // full-width block that would read as a trend it isn't.
  const barWidth = Math.min(width / series.length, 16);
  return (
    <svg className="sparkline" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={label}>
      {series.map((s, i) => (
        <g key={i}>
          <rect x={i * barWidth} y={height - (s.total / ceiling) * height} width={Math.max(barWidth - 1, 1)} height={(s.total / ceiling) * height} />
          {s.errors > 0 && (
            <rect className="errors" x={i * barWidth} y={height - (s.errors / ceiling) * height} width={Math.max(barWidth - 1, 1)} height={(s.errors / ceiling) * height} />
          )}
        </g>
      ))}
    </svg>
  );
}

export function ApplicationDetail() {
  const { id } = useParams<{ id: string }>();
  const { identity } = useIdentity();

  const [app, setApp] = useState<Application | null>(null);
  const [deployment, setDeployment] = useState<Deployment | null>(null);
  const [history, setHistory] = useState<Deployment[]>([]);
  const [build, setBuild] = useState<Build | null>(null);
  const [scaleEvents, setScaleEvents] = useState<ScaleEvent[]>([]);
  const [auditEntries, setAuditEntries] = useState<AuditEntry[]>([]);
  const [owners, setOwners] = useState<ApplicationOwner[]>([]);
  const [grantEmail, setGrantEmail] = useState("");
  const [grantRole, setGrantRole] = useState<OwnershipRole>("secondary");
  const [pendingTransfer, setPendingTransfer] = useState<OwnershipTransfer | null>(null);
  const [transferEmail, setTransferEmail] = useState("");
  const [secrets, setSecrets] = useState<SecretMetadata[]>([]);
  const [secretName, setSecretName] = useState("");
  const [secretValue, setSecretValue] = useState("");
  const [secretsForbidden, setSecretsForbidden] = useState(false);
  const [yamlDraft, setYamlDraft] = useState("");
  const [validationReport, setValidationReport] = useState<ValidationReport | null>(null);
  const [environment, setEnvironment] = useState<"dev" | "production">("dev");
  const [busy, setBusy] = useState<string | null>(null); // name of the in-flight action, for disabling buttons
  const [message, setMessage] = useState<{ kind: "error" | "info"; text: string } | null>(null);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [logCursor, setLogCursor] = useState<string | null>(null);
  const [logsLoaded, setLogsLoaded] = useState(false);
  const [logsError, setLogsError] = useState<string | null>(null);
  const [logsBusy, setLogsBusy] = useState(false);
  const [logFilter, setLogFilter] = useState("");
  const [logService, setLogService] = useState("");
  // Which of "no lines at all" and "none match what you asked for" to say.
  const [logFilterApplied, setLogFilterApplied] = useState(false);
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [metricsError, setMetricsError] = useState<string | null>(null);
  const [metricsBusy, setMetricsBusy] = useState(false);
  const [metricsRange, setMetricsRange] = useState<MetricsRange>("1h");

  const refresh = useCallback(async () => {
    if (!identity || !id) return;
    let freshApp: Application;
    try {
      freshApp = await getApplication(identity, id);
      setApp(freshApp);
      setYamlDraft((prev) => (prev === "" ? freshApp.deployment_yaml_draft ?? "" : prev));
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof ApiError ? err.message : String(err) });
      return;
    }
    // Unlike deployment/build/scale-events below, the audit trail exists
    // from the very first action (register, then validate) — Module W
    // audits those too — so this fetch isn't gated by lifecycle_status.
    queryAuditLog(identity, { resourceType: "application", resourceId: id })
      .then((r) => setAuditEntries(r.entries ?? []))
      .catch(() => setAuditEntries([]));

    // Same reasoning: Register always assigns a primary owner immediately,
    // so this exists from the first moment too.
    listOwners(identity, id)
      .then((r) => setOwners(r.owners ?? []))
      .catch(() => setOwners([]));

    getPendingOwnershipTransfer(identity, id)
      .then(setPendingTransfer)
      .catch(() => setPendingTransfer(null));

    // Names and versions only — see the Secrets block in api/client.ts.
    listSecrets(identity, id)
      .then((r) => {
        setSecrets(r.secrets ?? []);
        setSecretsForbidden(false);
      })
      .catch((err) => {
        setSecrets([]);
        // Owner-only server-side. Treating a 403 like every other failed
        // fetch here would tell a non-owner "No secrets yet" — which is
        // false: there may well be secrets, they just can't see them.
        setSecretsForbidden(err instanceof ApiError && err.status === 403);
      });

    // draft and validated are BOTH states no application-service method
    // ever transitions back into after a first build/deploy (checked
    // against every Go service's transition logic, not assumed) — so an
    // application in either one has, by construction, never had a build,
    // deployment, or scale event. Skip these calls entirely rather than
    // firing requests guaranteed to 404. Found via real browser testing:
    // Chrome logs every failed network request to the console regardless
    // of whether the resulting promise rejection is handled, so a fresh
    // registration (and its first validate) produced a wall of "Failed to
    // load resource: 404" noise even though the app's own behavior (empty
    // states) was already correct. Uses the freshly-fetched freshApp, not
    // the (still stale — React state updates aren't synchronous) app state
    // variable.
    if (freshApp.lifecycle_status === "draft" || freshApp.lifecycle_status === "validated") {
      setDeployment(null);
      setBuild(null);
      setHistory([]);
      setScaleEvents([]);
      return;
    }
    latestDeployment(identity, id).then(setDeployment).catch(() => setDeployment(null));
    latestBuild(identity, id).then(setBuild).catch(() => setBuild(null));
    deploymentHistory(identity, id).then(setHistory).catch(() => setHistory([]));
    listScaleEvents(identity, id)
      .then((r) => setScaleEvents(r.scale_events ?? []))
      .catch(() => setScaleEvents([]));
  }, [identity, id]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Logs load on their own rather than inside refresh(): they have their own
  // filters, and an action's refresh shouldn't throw away the page someone
  // is reading. draft and validated have never had a container to print
  // anything — the same reasoning as refresh()'s early return.
  const hasRun = app !== null && app.lifecycle_status !== "draft" && app.lifecycle_status !== "validated";

  useEffect(() => {
    if (!identity || !id || !hasRun) return;
    let cancelled = false;
    getLogs(identity, id, { limit: LOG_PAGE })
      .then((page) => {
        if (cancelled) return;
        setLogs(page.entries ?? []);
        setLogCursor(page.next_cursor ?? null);
        setLogsError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setLogs([]);
        setLogCursor(null);
        setLogsError(logsFailure(err));
      })
      .finally(() => {
        if (!cancelled) setLogsLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [identity, id, hasRun]);

  // The Platform API answers 404 for "no such application" and "not yours"
  // alike, so it never confirms to a stranger that an application exists
  // (FR-087). On this page the application has already loaded, so a 404
  // from the logs endpoint can only be the second — and saying so beats
  // repeating "application not found" on a page showing that application.
  function logsFailure(err: unknown): string {
    if (err instanceof ApiError && err.status === 404) return "Only this application's owners can read its logs.";
    return err instanceof ApiError ? err.message : String(err);
  }

  // cursor: the platform's own, for an older page, which is appended rather
  // than replacing what's on screen. contains/service override the fields
  // for Clear, whose state changes aren't readable here yet.
  async function fetchLogs(opts: { cursor?: string; contains?: string; service?: string } = {}) {
    if (!identity || !id) return;
    const contains = (opts.contains ?? logFilter).trim();
    const service = (opts.service ?? logService).trim();
    setLogsBusy(true);
    try {
      const page = await getLogs(identity, id, {
        contains: contains || undefined,
        service: service || undefined,
        limit: LOG_PAGE,
        cursor: opts.cursor,
      });
      setLogs((prev) => (opts.cursor ? [...prev, ...(page.entries ?? [])] : page.entries ?? []));
      setLogCursor(page.next_cursor ?? null);
      setLogsError(null);
    } catch (err) {
      if (!opts.cursor) setLogs([]);
      setLogsError(logsFailure(err));
    } finally {
      setLogFilterApplied(contains !== "" || service !== "");
      setLogsLoaded(true);
      setLogsBusy(false);
    }
  }

  // Same shape as the logs above, and the same reading of a 404: the
  // application is already on screen, so it can only mean "not yours".
  const loadMetrics = useCallback(
    async (range: MetricsRange) => {
      if (!identity || !id) return;
      setMetricsBusy(true);
      try {
        const from = new Date(Date.now() - METRICS_RANGES[range] * 60_000).toISOString();
        setMetrics(await getMetrics(identity, id, { from }));
        setMetricsError(null);
      } catch (err) {
        setMetrics(null);
        if (err instanceof ApiError && err.status === 404) {
          setMetricsError("Only this application's owners can read its metrics.");
        } else {
          setMetricsError(err instanceof ApiError ? err.message : String(err));
        }
      } finally {
        setMetricsBusy(false);
      }
    },
    [identity, id],
  );

  useEffect(() => {
    if (!hasRun) return;
    loadMetrics(metricsRange);
  }, [hasRun, loadMetrics, metricsRange]);

  async function runAction<T>(name: string, action: () => Promise<T>, successText: string) {
    setBusy(name);
    setMessage(null);
    try {
      await action();
      setMessage({ kind: "info", text: successText });
      await refresh();
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof ApiError ? `${err.code}: ${err.message}` : String(err) });
    } finally {
      setBusy(null);
    }
  }

  // Not routed through runAction: that helper swallows every error
  // internally and always resolves, so a caller can't tell success from
  // failure — fine for a button, wrong here, where a failed grant
  // shouldn't clear what the user just typed.
  async function handleGrantOwner(e: FormEvent) {
    e.preventDefault();
    if (!identity || !id) return;
    setBusy("grant-owner");
    setMessage(null);
    try {
      await grantOwner(identity, id, grantEmail, grantRole);
      setMessage({ kind: "info", text: `Granted access to ${grantEmail}.` });
      setGrantEmail("");
      await refresh();
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof ApiError ? `${err.code}: ${err.message}` : String(err) });
    } finally {
      setBusy(null);
    }
  }

  // Not routed through runAction, same reasoning as handleGrantOwner above:
  // a failed nomination shouldn't clear what the user just typed.
  async function handleInitiateTransfer(e: FormEvent) {
    e.preventDefault();
    if (!identity || !id) return;
    setBusy("initiate-transfer");
    setMessage(null);
    try {
      await initiateOwnershipTransfer(identity, id, transferEmail);
      setMessage({ kind: "info", text: `Nominated ${transferEmail} as the new owner — awaiting their acceptance.` });
      setTransferEmail("");
      await refresh();
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof ApiError ? `${err.code}: ${err.message}` : String(err) });
    } finally {
      setBusy(null);
    }
  }

  // Not routed through runAction, same reasoning as handleGrantOwner: a
  // failed save keeps what was typed so it can be corrected. A successful
  // one clears the value at once — the only copy of it this app ever holds
  // is the form field, for exactly as long as it's being typed. The success
  // message names the secret, never the value.
  async function handleSetSecret(e: FormEvent) {
    e.preventDefault();
    if (!identity || !id) return;
    setBusy("set-secret");
    setMessage(null);
    try {
      const saved = await setSecret(identity, id, secretName, secretValue);
      setSecretValue("");
      setSecretName("");
      setMessage({
        kind: "info",
        text: `Saved ${saved.name} (version ${saved.version}). It reaches the application at its next start — Restart applies it now.`,
      });
      await refresh();
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof ApiError ? `${err.code}: ${err.message}` : String(err) });
    } finally {
      setBusy(null);
    }
  }

  // FR-068. Not routed through runAction: the success message is the
  // server's own account of what happened (restarted or not), and an
  // incomplete rotation arrives as an error — rotation_incomplete — so it
  // reads as one.
  async function handleRotateSecret(name: string) {
    if (!identity || !id) return;
    const confirmed = window.confirm(
      `Rotate ${name}? The database gets a new password now — the old one stops working at once — and running instances are restarted onto it.`,
    );
    if (!confirmed) return;
    setBusy("rotate-secret");
    setMessage(null);
    try {
      const result = await rotateSecret(identity, id, name);
      setMessage({ kind: "info", text: `Rotated ${result.secret.name} (version ${result.secret.version}). ${result.note}` });
      await refresh();
    } catch (err) {
      setMessage({ kind: "error", text: err instanceof ApiError ? `${err.code}: ${err.message}` : String(err) });
    } finally {
      setBusy(null);
    }
  }

  if (!identity || !id) return null;
  if (!app) return <div className="page">{message ? <div className="error-banner">{message.text}</div> : <p>Loading…</p>}</div>;

  const canSuspend = app.lifecycle_status === "running";
  const canResume = app.lifecycle_status === "suspended";
  const canRestart = app.lifecycle_status === "running";
  const canArchive = app.lifecycle_status === "running" || app.lifecycle_status === "suspended";
  // Two routes into Deleted, mirroring lifecycle_service.go's Delete:
  // FR-050's own (archived or suspended), and straight from a state that
  // never went live — but only while nothing is actually serving or in
  // progress, since a rebuild leaves an application in Build with its
  // previous version still live. The server enforces this regardless.
  const somethingLive =
    history.some((d) => d.status === "running" || IN_FLIGHT_DEPLOYMENT.includes(d.status)) ||
    build?.status === "queued" ||
    build?.status === "in_progress";
  const neverWentLive =
    app.lifecycle_status === "draft" ||
    app.lifecycle_status === "validated" ||
    app.lifecycle_status === "build" ||
    app.lifecycle_status === "failed";
  const canDelete =
    app.lifecycle_status === "archived" || app.lifecycle_status === "suspended" || (neverWentLive && !somethingLive);
  const canDeploy = app.lifecycle_status === "running" || app.lifecycle_status === "build" || app.lifecycle_status === "failed";
  const canValidate = app.lifecycle_status === "draft";
  const canBuild = app.lifecycle_status === "validated" || app.lifecycle_status === "running" || app.lifecycle_status === "failed";
  const canSetSecret = app.lifecycle_status !== "deleted";

  return (
    <div className="page">
      <div className="page-header">
        <h1>
          {app.name} <StatusBadge status={app.lifecycle_status} />
        </h1>
      </div>

      {message && <div className={message.kind === "error" ? "error-banner" : "info-banner"}>{message.text}</div>}

      <section className="card">
        <h2>Overview</h2>
        <dl className="detail-grid">
          <dt>Description</dt>
          <dd>{app.description || <em>none</em>}</dd>
          <dt>Created</dt>
          <dd>{new Date(app.created_at).toLocaleString()}</dd>
          <dt>Updated</dt>
          <dd>{new Date(app.updated_at).toLocaleString()}</dd>
          {deployment?.status === "running" && deployment.containers && (
            <>
              <dt>Live URL(s)</dt>
              <dd>
                {Object.entries(deployment.containers).map(([svc, c]) => (
                  <div key={svc}>
                    <a href={c.url} target="_blank" rel="noreferrer">
                      {c.url}
                    </a>{" "}
                    <span className="hint">({svc})</span>
                  </div>
                ))}
              </dd>
            </>
          )}
        </dl>
      </section>

      <section className="card">
        <h2>deployment.yaml</h2>
        <textarea
          className="yaml-editor"
          value={yamlDraft}
          onChange={(e) => setYamlDraft(e.target.value)}
          rows={12}
          spellCheck={false}
        />
        <div className="action-row">
          <button
            disabled={busy !== null}
            onClick={() => runAction("save", () => saveDeploymentYaml(identity, id, yamlDraft), "Saved (reverted to draft).")}
          >
            Save draft
          </button>
          <button
            disabled={busy !== null || !canValidate}
            title={canValidate ? undefined : "Only callable from draft"}
            onClick={() =>
              runAction(
                "validate",
                async () => {
                  const res = await validateApplication(identity, id);
                  setValidationReport(res.report);
                  if (!res.report.valid) throw new Error("Validation failed — see findings below.");
                },
                "Validation passed.",
              )
            }
          >
            Validate
          </button>
        </div>
        {validationReport && (
          <ul className="findings">
            {validationReport.checks.map((c) => (
              <li key={c.name} className={`finding-${c.status}`}>
                <strong>{c.name}</strong>: {c.status}
                {c.details && c.details.length > 0 && (
                  <ul>
                    {c.details.map((d, i) => (
                      <li key={i}>{d}</li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="card">
        <h2>Build</h2>
        <p className="hint">
          Uploads the source archive directly — a tar.gz with a top-level directory per service name, matching{" "}
          <code>deployment.yaml</code>'s <code>services</code> keys.
        </p>
        <input
          type="file"
          accept=".tar.gz,.tgz,application/gzip"
          disabled={busy !== null || !canBuild}
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            runAction("build", () => triggerBuild(identity, id, file), "Build finished — see status below.");
            e.target.value = "";
          }}
        />
        {!canBuild && <p className="hint">Only callable from validated, running, or failed.</p>}
        {build && (
          <p>
            Latest build: <StatusBadge status={build.status} />{" "}
            {build.error_detail && <span className="error-text">{build.error_detail}</span>}
          </p>
        )}
      </section>

      <section className="card">
        <h2>Deploy</h2>
        <div className="action-row">
          <select value={environment} onChange={(e) => setEnvironment(e.target.value as "dev" | "production")}>
            <option value="dev">dev</option>
            <option value="production">production</option>
          </select>
          <button
            disabled={busy !== null || !canDeploy}
            title={canDeploy ? undefined : "Requires a successful build"}
            onClick={() => runAction("deploy", () => deployApplication(identity, id, environment), "Deploy request sent.")}
          >
            Deploy
          </button>
        </div>
        {deployment && (
          <p>
            Latest deployment: <StatusBadge status={deployment.status} />
            {deployment.failure_reason && <span className="error-text"> — {deployment.failure_reason}</span>}
            {deployment.rejection_reason && <span className="error-text"> — {deployment.rejection_reason}</span>}
          </p>
        )}
      </section>

      <section className="card">
        <h2>Lifecycle actions</h2>
        <div className="action-row">
          <button disabled={busy !== null || !canSuspend} onClick={() => runAction("suspend", () => suspendApplication(identity, id), "Suspended.")}>
            Suspend
          </button>
          <button disabled={busy !== null || !canResume} onClick={() => runAction("resume", () => resumeApplication(identity, id), "Resumed.")}>
            Resume
          </button>
          <button disabled={busy !== null || !canRestart} onClick={() => runAction("restart", () => restartApplication(identity, id), "Restarted.")}>
            Restart
          </button>
          <button disabled={busy !== null || !canArchive} onClick={() => runAction("archive", () => archiveApplication(identity, id), "Archived.")}>
            Archive
          </button>
          <button
            className="button-danger"
            disabled={busy !== null || !canDelete}
            title={
              canDelete
                ? undefined
                : "Requires archived or suspended — or, for an application that never went live, nothing still running or in progress"
            }
            onClick={() => {
              const confirmed = window.confirm(
                `Type-confirm: delete "${app.name}"? This is irreversible. Click OK only if you're certain.`,
              );
              if (!confirmed) return;
              runAction("delete", () => deleteApplication(identity, id), "Deleted.");
            }}
          >
            Delete
          </button>
        </div>
      </section>

      <section className="card">
        <h2>Owners</h2>
        <p className="hint">
          Only the primary owner can grant or revoke access — this page doesn't hide the form from anyone else (there's
          no way to resolve "am I the primary owner" client-side without an extra lookup), so a non-primary owner
          attempting this sees the real error below.
        </p>
        <table className="data-table">
          <thead>
            <tr>
              <th>User ID</th>
              <th>Role</th>
              <th>Status</th>
              <th>Assigned</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {owners.map((o) => (
              <tr key={o.user_id + o.ownership_role}>
                <td>{o.user_id}</td>
                <td>{o.ownership_role}</td>
                <td>
                  <StatusBadge status={o.status === "active" ? "success" : "failure"} />
                </td>
                <td>{new Date(o.assigned_at).toLocaleString()}</td>
                <td>
                  {o.ownership_role !== "primary" && o.status === "active" && (
                    <button
                      disabled={busy !== null}
                      onClick={() =>
                        runAction("revoke-owner", () => revokeOwner(identity, id, o.user_id), "Access revoked.")
                      }
                    >
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <form className="action-row" onSubmit={handleGrantOwner}>
          <input
            type="email"
            required
            placeholder="teammate@example.com"
            value={grantEmail}
            onChange={(e) => setGrantEmail(e.target.value)}
            aria-label="Email of the employee to grant access to"
          />
          <select value={grantRole} onChange={(e) => setGrantRole(e.target.value as OwnershipRole)} aria-label="Access level to grant">
            <option value="secondary">Co-owner</option>
            <option value="technical">Contributor</option>
          </select>
          <button type="submit" disabled={busy !== null}>
            Grant access
          </button>
        </form>

        <h3>Transfer primary ownership</h3>
        {pendingTransfer ? (
          <>
            <p className="hint">
              A transfer to <code>{pendingTransfer.to_user_id}</code> is pending — only they can accept it (this page
              doesn't hide the button from anyone else viewing it), and it expires{" "}
              {new Date(pendingTransfer.expires_at).toLocaleString()}.
            </p>
            <button
              disabled={busy !== null}
              onClick={() =>
                runAction("accept-transfer", () => acceptOwnershipTransfer(identity, pendingTransfer.id), "Ownership transfer accepted.")
              }
            >
              Accept transfer
            </button>
          </>
        ) : (
          <form className="action-row" onSubmit={handleInitiateTransfer}>
            <input
              type="email"
              required
              placeholder="new-owner@example.com"
              value={transferEmail}
              onChange={(e) => setTransferEmail(e.target.value)}
              aria-label="Email of the employee to nominate as the new primary owner"
            />
            <button type="submit" disabled={busy !== null}>
              Nominate new owner
            </button>
          </form>
        )}
      </section>

      <section className="card">
        <h2>Secrets</h2>
        <p className="hint">
          API keys and tokens this application needs. Each one is injected into every container it starts, as an
          environment variable of the same name. Values are <strong>write-only</strong>: once saved, nothing — this
          page included — can show one again. A new or changed value reaches the application at its next start;
          Restart applies it now.
        </p>
        {secretsForbidden && <p className="hint">Only this application's owners can see or change its secrets.</p>}
        {!secretsForbidden && secrets.length === 0 && <p className="hint">No secrets yet.</p>}
        {secrets.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Managed by</th>
                <th>Version</th>
                <th>Last set</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {secrets.map((s) => (
                <tr key={s.name}>
                  <td>
                    <code>{s.name}</code>
                  </td>
                  <td>{s.managed_by === "platform" ? "Platform" : "Owner"}</td>
                  <td>{s.version}</td>
                  <td>{new Date(s.updated_at).toLocaleString()}</td>
                  <td>
                    {s.managed_by === "platform" && (
                      <button disabled={busy !== null || !canSetSecret} onClick={() => handleRotateSecret(s.name)}>
                        Rotate
                      </button>
                    )}
                    {s.managed_by === "employee" && (
                      <button
                        disabled={busy !== null}
                        onClick={() => {
                          if (!window.confirm(`Delete ${s.name}? The application loses it at its next start.`)) return;
                          runAction("delete-secret", () => deleteSecret(identity, id, s.name), `Deleted ${s.name}.`);
                        }}
                      >
                        Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {secrets.some((s) => s.managed_by === "platform") && (
          <p className="hint">
            Platform-managed secrets are generated by the platform itself — today, the password for this application's
            database — and can't be changed or deleted here, only rotated: Rotate gives the database a new password, the
            old one stops working at once, and running instances are restarted onto the new one.
          </p>
        )}
        <form className="secret-form" onSubmit={handleSetSecret}>
          <input
            required
            pattern="[A-Z][A-Z0-9_]{0,63}"
            title="Uppercase letters, digits and underscores, starting with a letter — it becomes an environment variable name"
            placeholder="API_KEY"
            value={secretName}
            onChange={(e) => setSecretName(e.target.value)}
            aria-label="Secret name"
            autoComplete="off"
            spellCheck={false}
          />
          {/* A textarea, not a password input: plenty of credentials are
              multi-line (PEM keys, JSON service-account files), and a
              password field invites the browser's password manager to save
              the value. Spell-check is off because enhanced spell-checking
              can send what's typed to a third-party service. */}
          <textarea
            required
            rows={2}
            placeholder="Value — write-only once saved"
            value={secretValue}
            onChange={(e) => setSecretValue(e.target.value)}
            aria-label="Secret value"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
          />
          <button type="submit" disabled={busy !== null || !canSetSecret}>
            Save secret
          </button>
        </form>
      </section>

      <section className="card">
        <h2>Deployment history</h2>
        {history.length === 0 && <p className="hint">No deployments yet.</p>}
        {history.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th>Status</th>
                <th>Environment</th>
                <th>Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {history.map((d) => (
                <tr key={d.id}>
                  <td>
                    <StatusBadge status={d.status} />
                  </td>
                  <td>{d.environment}</td>
                  <td>{new Date(d.created_at).toLocaleString()}</td>
                  <td>
                    {(d.status === "running" || d.status === "superseded") && d.id !== deployment?.id && (
                      <button
                        disabled={busy !== null}
                        onClick={() =>
                          runAction("rollback", () => rollbackApplication(identity, id, d.id), "Rolled back.")
                        }
                      >
                        Roll back to this
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="card">
        <h2>Scale events</h2>
        {scaleEvents.length === 0 && <p className="hint">None recorded for the current deployment.</p>}
        {scaleEvents.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th>Service</th>
                <th>Direction</th>
                <th>Reason</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {scaleEvents.map((e, i) => (
                <tr key={i}>
                  <td>{e.service_name}</td>
                  <td>{e.direction}</td>
                  <td>{e.trigger_reason}</td>
                  <td>{new Date(e.occurred_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="card">
        <h2>Metrics</h2>
        <p className="hint">
          What the platform measured: CPU and memory read from this application's containers, and requests, errors and
          latency counted at the platform's own address for it — nothing is asked of the application itself. An error is
          a 5xx or no answer at all; a 404 is a request. Latency is a mean and a max, never a percentile. Only this
          application's owners can read them.
        </p>
        {!hasRun && <p className="hint">Nothing has run yet. Measurements start once a deploy starts a container.</p>}
        {hasRun && (
          <>
            <div className="toolbar">
              {(Object.keys(METRICS_RANGES) as MetricsRange[]).map((range) => (
                <button
                  key={range}
                  type="button"
                  className={range === metricsRange ? "button-primary" : undefined}
                  disabled={metricsBusy}
                  onClick={() => setMetricsRange(range)}
                >
                  Last {range}
                </button>
              ))}
              <button type="button" disabled={metricsBusy} onClick={() => loadMetrics(metricsRange)}>
                {metricsBusy ? "Loading…" : "Refresh"}
              </button>
            </div>
            {metricsError && <p className="error-text">{metricsError}</p>}
            {metrics && !metrics.collection.collecting && (
              <p className="error-text">
                {metrics.collection.note || "The platform's own sampling is behind, so this may be incomplete."}
              </p>
            )}
            {metrics && metrics.collection.collecting && metrics.collection.note && (
              <p className="hint">{metrics.collection.note}</p>
            )}
            {metrics && (
              <>
                <dl className="metric-tiles">
                  <div>
                    <dt>Requests</dt>
                    <dd>{metrics.summary.requests}</dd>
                  </div>
                  <div>
                    <dt>Errors</dt>
                    <dd>
                      {metrics.summary.errors}{" "}
                      <span className="metric-sub">{Math.round(metrics.summary.error_rate * 100)}%</span>
                    </dd>
                  </div>
                  <div>
                    <dt>Latency</dt>
                    <dd>
                      {metrics.summary.latency_ms_mean} ms{" "}
                      <span className="metric-sub">max {metrics.summary.latency_ms_max} ms</span>
                    </dd>
                  </div>
                  <div>
                    <dt>CPU now</dt>
                    <dd>{metrics.summary.cpu_percent_latest}%</dd>
                  </div>
                  <div>
                    <dt>Memory now</dt>
                    <dd>{formatBytes(metrics.summary.memory_bytes_latest)}</dd>
                  </div>
                </dl>
                {metrics.resource.length === 0 && metrics.traffic.length === 0 && (
                  <p className="hint">No measurements in this window.</p>
                )}
                {metrics.resource.length > 1 && (
                  <figure className="metric-chart">
                    <figcaption>CPU %, oldest first</figcaption>
                    <Sparkline
                      label={`CPU percent over the last ${metricsRange}`}
                      values={[...metrics.resource].reverse().map((point) => point.cpu_percent)}
                    />
                  </figure>
                )}
                {metrics.traffic.length > 0 && (
                  <figure className="metric-chart">
                    <figcaption>
                      Requests per minute, failures in red ({metrics.summary.errors} of {metrics.summary.requests}{" "}
                      failed)
                    </figcaption>
                    <Bars
                      label={`Requests per minute over the last ${metricsRange}`}
                      series={[...metrics.traffic].reverse().map((b) => ({ total: b.requests, errors: b.errors }))}
                    />
                  </figure>
                )}
                <p className="hint">
                  {metrics.instances.length > 0
                    ? metrics.instances
                        .map((i) =>
                          i.scaled_to_zero
                            ? `${i.service}: scaled to zero`
                            : `${i.service}: ${i.instances} instance${i.instances === 1 ? "" : "s"}`,
                        )
                        .join(", ")
                    : "Nothing running."}
                  {metrics.last_scale_event &&
                    ` · last scale event: ${metrics.last_scale_event.service} ${metrics.last_scale_event.direction} (${metrics.last_scale_event.reason}) at ${new Date(metrics.last_scale_event.occurred_at).toLocaleString()}`}
                </p>
              </>
            )}
          </>
        )}
      </section>

      <section className="card">
        <h2>Logs</h2>
        <p className="hint">
          What this application's containers printed, newest first. A secret the platform injected appears as{" "}
          <code>[REDACTED:NAME]</code>: it is replaced before the line is stored, so it never reaches this page. Lines
          outlive the container that wrote them — a failed deploy's included. Only this application's owners can read
          them.
        </p>
        {!hasRun && <p className="hint">Nothing has run yet. Logs appear once a deploy starts a container.</p>}
        {hasRun && (
          <>
            <form
              className="toolbar"
              onSubmit={(e) => {
                e.preventDefault();
                fetchLogs();
              }}
            >
              <input
                aria-label="Search log text"
                placeholder="Search text"
                value={logFilter}
                onChange={(e) => setLogFilter(e.target.value)}
              />
              <input
                aria-label="Filter by service"
                placeholder="Service"
                value={logService}
                onChange={(e) => setLogService(e.target.value)}
              />
              <button type="submit" disabled={logsBusy}>
                Search
              </button>
              <button type="button" disabled={logsBusy} onClick={() => fetchLogs()}>
                {logsBusy ? "Loading…" : "Refresh"}
              </button>
              {(logFilter !== "" || logService !== "") && (
                <button
                  type="button"
                  disabled={logsBusy}
                  onClick={() => {
                    setLogFilter("");
                    setLogService("");
                    fetchLogs({ contains: "", service: "" });
                  }}
                >
                  Clear
                </button>
              )}
            </form>
            {logsError && <p className="error-text">{logsError}</p>}
            {!logsError && logsLoaded && logs.length === 0 && (
              <p className="hint">{logFilterApplied ? "No lines match this filter." : "No log lines yet."}</p>
            )}
            {logs.length > 0 && (
              <ol className="log-lines">
                {logs.map((l, i) => (
                  <li
                    key={`${l.timestamp}-${l.instance}-${i}`}
                    className={l.stream === "stderr" ? "log-line log-stderr" : "log-line"}
                  >
                    <time dateTime={l.timestamp}>{new Date(l.timestamp).toLocaleTimeString()}</time>
                    {/* Never the colour alone: the stream is spelled out. */}
                    <span className="log-stream">{l.stream === "stderr" ? "err" : "out"}</span>
                    <span className="log-service" title={`container ${l.instance}`}>
                      {l.service}
                    </span>
                    <span className="log-message">{l.message}</span>
                  </li>
                ))}
              </ol>
            )}
            {logCursor && (
              <button disabled={logsBusy} onClick={() => fetchLogs({ cursor: logCursor })}>
                Load older lines
              </button>
            )}
          </>
        )}
      </section>

      <section className="card">
        <h2>Audit log</h2>
        <p className="hint">
          Every recorded action for this application — see the platform-wide{" "}
          <a href="/audit-log">Audit Log</a> for actions across every application you own.
        </p>
        {auditEntries.length === 0 && <p className="hint">No audit entries yet.</p>}
        {auditEntries.length > 0 && (
          <table className="data-table">
            <thead>
              <tr>
                <th>When</th>
                <th>Action</th>
                <th>Outcome</th>
                <th>Detail</th>
              </tr>
            </thead>
            <tbody>
              {auditEntries.map((e) => (
                <tr key={e.id}>
                  <td>{new Date(e.occurred_at).toLocaleString()}</td>
                  <td>{e.action}</td>
                  <td>
                    <StatusBadge status={e.outcome} />
                  </td>
                  <td>{e.detail || <em>—</em>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
