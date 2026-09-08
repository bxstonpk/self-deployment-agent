import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listNotifications, markNotificationRead } from "../api/client";
import { ApiError } from "../api/types";
import type { Notification } from "../api/types";
import { useIdentity } from "../context/IdentityContext";

export function Notifications() {
  const { identity } = useIdentity();
  const [notifications, setNotifications] = useState<Notification[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

  const refresh = useCallback(() => {
    if (!identity) return;
    setError(null);
    listNotifications(identity, unreadOnly)
      .then((data) => setNotifications(data.notifications ?? []))
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)));
  }, [identity, unreadOnly]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function handleMarkRead(id: string) {
    if (!identity) return;
    setBusyId(id);
    setError(null);
    try {
      await markNotificationRead(identity, id);
      refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  }

  if (!identity) return null;

  return (
    <div className="page">
      <div className="page-header">
        <h1>Notifications</h1>
      </div>
      <p className="hint">
        Deployment status and production-approval events for applications you own. In-app only — there's no
        email/Slack delivery yet.
      </p>

      {error && <div className="error-banner">{error}</div>}

      <div className="toolbar">
        <label>
          <input type="checkbox" checked={unreadOnly} onChange={(e) => setUnreadOnly(e.target.checked)} /> Unread only
        </label>
      </div>

      {notifications === null && !error && <p>Loading…</p>}
      {notifications !== null && notifications.length === 0 && <p className="hint">Nothing here.</p>}

      {notifications !== null && notifications.length > 0 && (
        <table className="data-table">
          <thead>
            <tr>
              <th>When</th>
              <th>Category</th>
              <th>Title</th>
              <th>Detail</th>
              <th>Resource</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {notifications.map((n) => (
              <tr key={n.id} className={n.read_at ? undefined : "notification-unread"}>
                <td>{new Date(n.created_at).toLocaleString()}</td>
                <td>{n.category}</td>
                <td>{n.title}</td>
                <td>{n.detail || <em>—</em>}</td>
                <td>
                  {n.resource_type}
                  {n.resource_id ? ` / ${n.resource_id}` : ""}
                </td>
                <td>
                  {!n.read_at && (
                    <button disabled={busyId !== null} onClick={() => handleMarkRead(n.id)}>
                      {busyId === n.id ? "Marking…" : "Mark read"}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <p className="hint">
        See the <Link to="/audit-log">Audit Log</Link> for a complete record of every action, not just the ones that
        notify.
      </p>
    </div>
  );
}
