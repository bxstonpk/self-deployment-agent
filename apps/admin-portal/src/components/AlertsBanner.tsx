import { useEffect, useState } from "react";
import { getAlerts } from "../api/client";
import type { ApplicationAlerts } from "../api/types";
import type { Identity } from "../identity";

// What is firing for this application right now (FR-092, DEC-028). The
// notifications themselves arrive in the inbox; this is the current state,
// so an owner opening the page sees it without hunting for them.
export function AlertsBanner({ identity, applicationId }: { identity: Identity; applicationId: string }) {
  const [alerts, setAlerts] = useState<ApplicationAlerts | null>(null);

  useEffect(() => {
    let cancelled = false;
    getAlerts(identity, applicationId)
      .then((a) => {
        if (!cancelled) setAlerts(a);
      })
      .catch(() => {
        // Not an owner, or alerts unavailable: nothing to show.
      });
    return () => {
      cancelled = true;
    };
  }, [identity, applicationId]);

  if (!alerts || alerts.active.length === 0) return null;
  return (
    <div className="error-banner" role="alert" aria-label="Active alerts">
      <strong>
        {alerts.active.length === 1 ? "1 alert is firing" : `${alerts.active.length} alerts are firing`}
      </strong>
      <ul>
        {alerts.active.map((a) => (
          <li key={`${a.service}:${a.rule}`}>
            <span className={a.level === "critical" ? "badge badge-danger" : "badge badge-warning"}>{a.level}</span>{" "}
            {a.message} — since {new Date(a.started_at).toLocaleString()}
          </li>
        ))}
      </ul>
    </div>
  );
}
