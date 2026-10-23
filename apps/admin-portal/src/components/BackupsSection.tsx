import { useCallback, useEffect, useState } from "react";
import { listBackups, restoreBackup, takeBackup } from "../api/client";
import { ApiError, type BackupOverview, type DatabaseBackup } from "../api/types";
import type { Identity } from "../identity";

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const TRIGGER_LABEL: Record<DatabaseBackup["trigger"], string> = {
  scheduled: "scheduled",
  manual: "on request",
  pre_restore: "before a restore",
};

// FR-064 with DEC-025's policy: a full backup every Sunday, a differential
// every other day, and the previous week's set deleted once a new full
// succeeds. Owners can take one now and restore any of them; a restore
// needs the application's exact name, and first backs up what it replaces.
export function BackupsSection({ identity, applicationId, applicationName }: {
  identity: Identity;
  applicationId: string;
  applicationName: string;
}) {
  const [overview, setOverview] = useState<BackupOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [restoring, setRestoring] = useState<DatabaseBackup | null>(null);
  const [confirm, setConfirm] = useState("");

  const load = useCallback(async () => {
    try {
      setOverview(await listBackups(identity, applicationId));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }, [identity, applicationId]);

  useEffect(() => {
    void load();
  }, [load]);

  async function run(action: () => Promise<string>) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      setNotice(await action());
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const backUpNow = () =>
    run(async () => {
      const { backup } = await takeBackup(identity, applicationId);
      return `Took a ${backup.kind} backup (${size(backup.stored_bytes)}).`;
    });

  const restore = (b: DatabaseBackup) =>
    run(async () => {
      const out = await restoreBackup(identity, applicationId, b.id, confirm);
      setRestoring(null);
      setConfirm("");
      return `Restored the ${b.kind} backup from ${new Date(b.taken_at).toLocaleString()}. What it replaced was backed up first (${out.pre_restore_backup.id.slice(0, 8)}).`;
    });

  return (
    <section className="card">
      <h2>Backups</h2>
      {overview && (
        <p className="hint">
          {overview.policy === "none"
            ? "This application's deployment.yaml sets database.backup: none, so it is not backed up on a schedule."
            : `A full backup every ${overview.schedule.full_weekday} and a differential every other day, from ${String(
                overview.schedule.hour,
              ).padStart(2, "0")}:00 (${overview.schedule.timezone}). Once a new full backup succeeds, the previous week's backups are deleted.`}
        </p>
      )}
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {notice && (
        <div className="info-banner" role="status">
          {notice}
        </div>
      )}
      {overview === null ? (
        <p>Loading…</p>
      ) : overview.backups.length === 0 ? (
        <p>No backups yet.</p>
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Taken</th>
              <th>Kind</th>
              <th>Why</th>
              <th>Status</th>
              <th>Stored</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {overview.backups.map((b) => (
              <tr key={b.id}>
                <td>{new Date(b.taken_at).toLocaleString()}</td>
                <td>{b.kind}</td>
                <td>{TRIGGER_LABEL[b.trigger]}</td>
                <td>
                  {b.status === "succeeded" ? (
                    <span className="badge badge-success">succeeded</span>
                  ) : (
                    <span className="badge badge-danger" title={b.error}>
                      failed
                    </span>
                  )}
                </td>
                <td>{b.status === "succeeded" ? size(b.stored_bytes) : b.error}</td>
                <td>
                  {b.status === "succeeded" && (
                    <button disabled={busy} onClick={() => setRestoring(b)}>
                      Restore…
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {restoring && (
        <div className="card" role="dialog" aria-label="Confirm restore">
          <p>
            Restoring replaces <strong>everything</strong> in this application's database with the {restoring.kind}{" "}
            backup from {new Date(restoring.taken_at).toLocaleString()}. The current contents are backed up first.
          </p>
          <label>
            Type <code>{applicationName}</code> to confirm
            <input value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
          </label>
          <div className="action-row">
            <button
              className="button-danger"
              disabled={busy || confirm !== applicationName}
              onClick={() => restore(restoring)}
            >
              Restore this backup
            </button>
            <button
              onClick={() => {
                setRestoring(null);
                setConfirm("");
              }}
            >
              Cancel
            </button>
          </div>
        </div>
      )}
      <div className="action-row">
        <button disabled={busy} onClick={backUpNow}>
          {busy ? "Working…" : "Back up now"}
        </button>
      </div>
    </section>
  );
}
