import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { listApplications } from "../api/client";
import type { ApiError } from "../api/types";
import type { Application, LifecycleStatus } from "../api/types";
import { useIdentity } from "../context/IdentityContext";
import { StatusBadge } from "../components/StatusBadge";

export function ApplicationList() {
  const { identity } = useIdentity();
  const [applications, setApplications] = useState<Application[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<LifecycleStatus | "all">("all");
  const [search, setSearch] = useState("");

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    listApplications(identity)
      .then((data) => {
        if (!cancelled) setApplications(data.applications ?? []);
      })
      .catch((err: ApiError) => {
        if (!cancelled) setError(err.message);
      });
    return () => {
      cancelled = true;
    };
  }, [identity]);

  const filtered = useMemo(() => {
    if (!applications) return [];
    return applications.filter((app) => {
      if (statusFilter !== "all" && app.lifecycle_status !== statusFilter) return false;
      if (search && !app.name.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [applications, statusFilter, search]);

  if (!identity) return null;

  return (
    <div className="page">
      <div className="page-header">
        <h1>Applications</h1>
        <Link to="/applications/new" className="button-primary">
          Register application
        </Link>
      </div>

      {error && <div className="error-banner">{error}</div>}

      <div className="toolbar">
        <input
          placeholder="Search by name…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          aria-label="Search applications by name"
        />
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as LifecycleStatus | "all")}
          aria-label="Filter by lifecycle status"
        >
          <option value="all">All statuses</option>
          {(
            [
              "draft",
              "validated",
              "build",
              "deploying",
              "running",
              "suspended",
              "rolled_back",
              "failed",
              "archived",
              "deleted",
            ] as LifecycleStatus[]
          ).map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </div>

      {applications === null && !error && <p>Loading…</p>}
      {applications !== null && filtered.length === 0 && <p className="hint">No applications match.</p>}

      {filtered.length > 0 && (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((app) => (
              <tr key={app.id}>
                <td>
                  <Link to={`/applications/${app.id}`}>{app.name}</Link>
                </td>
                <td>
                  <StatusBadge status={app.lifecycle_status} />
                </td>
                <td>{new Date(app.created_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
