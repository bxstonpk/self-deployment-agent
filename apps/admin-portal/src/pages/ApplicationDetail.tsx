import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import {
  acceptOwnershipTransfer,
  archiveApplication,
  deleteApplication,
  deployApplication,
  deploymentHistory,
  getApplication,
  getPendingOwnershipTransfer,
  grantOwner,
  initiateOwnershipTransfer,
  latestBuild,
  latestDeployment,
  listOwners,
  listScaleEvents,
  queryAuditLog,
  restartApplication,
  resumeApplication,
  revokeOwner,
  rollbackApplication,
  saveDeploymentYaml,
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
  OwnershipRole,
  OwnershipTransfer,
  ScaleEvent,
  ValidationReport,
} from "../api/types";
import { useIdentity } from "../context/IdentityContext";
import { StatusBadge } from "../components/StatusBadge";

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
  const [yamlDraft, setYamlDraft] = useState("");
  const [validationReport, setValidationReport] = useState<ValidationReport | null>(null);
  const [environment, setEnvironment] = useState<"dev" | "production">("dev");
  const [busy, setBusy] = useState<string | null>(null); // name of the in-flight action, for disabling buttons
  const [message, setMessage] = useState<{ kind: "error" | "info"; text: string } | null>(null);

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

  if (!identity || !id) return null;
  if (!app) return <div className="page">{message ? <div className="error-banner">{message.text}</div> : <p>Loading…</p>}</div>;

  const canSuspend = app.lifecycle_status === "running";
  const canResume = app.lifecycle_status === "suspended";
  const canRestart = app.lifecycle_status === "running";
  const canArchive = app.lifecycle_status === "running" || app.lifecycle_status === "suspended";
  const canDelete = app.lifecycle_status === "archived" || app.lifecycle_status === "suspended";
  const canDeploy = app.lifecycle_status === "running" || app.lifecycle_status === "build" || app.lifecycle_status === "failed";
  const canValidate = app.lifecycle_status === "draft";
  const canBuild = app.lifecycle_status === "validated" || app.lifecycle_status === "running" || app.lifecycle_status === "failed";

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
            title={canDelete ? undefined : "Requires archived or suspended"}
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
