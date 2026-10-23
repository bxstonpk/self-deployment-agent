import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { getAdminOverview, type AdminApplication, type AdminOverview } from "../api/client";
import { ApiError } from "../api/types";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";

// The administrators' dashboard: a task manager for the platform. The host's
// CPU, memory and disk, traffic across every application, and every
// application with what it's using now. Refreshed every few seconds while
// the page is open.

const REFRESH_MS = 5_000;

type SortKey = "name" | "status" | "instances" | "cpu_percent" | "memory_bytes" | "requests" | "errors" | "latency_ms_avg";

const COLUMNS: { key: SortKey; label: string; numeric: boolean }[] = [
  { key: "name", label: "Application", numeric: false },
  { key: "status", label: "Status", numeric: false },
  { key: "instances", label: "Instances", numeric: true },
  { key: "cpu_percent", label: "CPU", numeric: true },
  { key: "memory_bytes", label: "Memory", numeric: true },
  { key: "requests", label: "Requests", numeric: true },
  { key: "errors", label: "Errors", numeric: true },
  { key: "latency_ms_avg", label: "Avg latency", numeric: true },
];

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86_400);
  const h = Math.floor((seconds % 86_400) / 3_600);
  const m = Math.floor((seconds % 3_600) / 60);
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`;
}

function pct(n: number): string {
  const r = Math.round(n * 10) / 10;
  return `${r >= 10 ? Math.round(r) : r.toFixed(1)}%`;
}

// Level for colouring a reading: the hotter, the stronger the colour.
function level(percent: number): "ok" | "warm" | "hot" {
  return percent >= 85 ? "hot" : percent >= 60 ? "warm" : "ok";
}

function Sparkline({ values, max, label }: { values: number[]; max: number; label: string }) {
  if (values.length < 2) {
    return <div className="dash-spark dash-spark-empty">Collecting…</div>;
  }
  const top = Math.max(max, ...values, 1);
  const step = 100 / (values.length - 1);
  const points = values.map((v, i) => `${(i * step).toFixed(2)},${(40 - (v / top) * 38).toFixed(2)}`);
  return (
    <svg className="dash-spark" viewBox="0 0 100 40" preserveAspectRatio="none" role="img" aria-label={label}>
      <polygon points={`0,40 ${points.join(" ")} 100,40`} className="dash-spark-area" />
      <polyline points={points.join(" ")} className="dash-spark-line" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function Meter({ percent }: { percent: number }) {
  return (
    <div className={`dash-meter dash-${level(percent)}`} aria-hidden="true">
      <span style={{ width: `${Math.min(100, Math.max(0, percent))}%` }} />
    </div>
  );
}

export function Dashboard() {
  const { identity } = useIdentity();
  const [data, setData] = useState<AdminOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [paused, setPaused] = useState(false);
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: "cpu_percent", desc: true });

  const load = useCallback(() => {
    if (!identity) return;
    getAdminOverview(identity)
      .then((o) => {
        setData(o);
        setError(null);
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 403) setForbidden(true);
        else setError(err instanceof Error ? err.message : String(err));
      });
  }, [identity]);

  useEffect(() => {
    load();
    if (paused) return;
    const t = setInterval(load, REFRESH_MS);
    return () => clearInterval(t);
  }, [load, paused]);

  const apps = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const list = (data?.applications ?? []).filter(
      (a) => !q || a.name.includes(q) || a.owner_email.toLowerCase().includes(q) || a.department.toLowerCase().includes(q),
    );
    const dir = sort.desc ? -1 : 1;
    return [...list].sort((a, b) => {
      const x = a[sort.key];
      const y = b[sort.key];
      const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
      return c === 0 ? a.name.localeCompare(b.name) : c * dir;
    });
  }, [data, filter, sort]);

  if (forbidden) {
    return (
      <div className="page">
        <div className="page-header">
          <h1>Dashboard</h1>
        </div>
        <div className="error-banner" role="alert">Only platform administrators can see the dashboard.</div>
      </div>
    );
  }

  const host = data?.host;
  const memPercent = host && host.memory_total_bytes > 0 ? (100 * host.memory_used_bytes) / host.memory_total_bytes : 0;
  const history = data?.host_history ?? [];
  const traffic = data?.traffic ?? [];
  // The latest minute only counts as "now" if it's the current or the
  // previous minute; a quiet spell means no traffic, not the last busy minute.
  const latest = traffic.length > 0 ? traffic[traffic.length - 1] : null;
  const lastMinute =
    latest && data && Date.parse(data.generated_at) - Date.parse(latest.minute) < 120_000 ? latest : null;
  const running = (data?.applications ?? []).filter((a) => a.status === "running");
  const instances = (data?.applications ?? []).reduce((n, a) => n + a.instances, 0);
  const windowMin = Math.round((data?.traffic_window_seconds ?? 300) / 60);
  const cpuCapacity = (host?.cpu_count || 1) * 100;

  const toggleSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, desc: !s.desc } : { key, desc: key !== "name" && key !== "status" }));

  return (
    <div className="page dash">
      <div className="page-header">
        <div>
          <h1>Dashboard</h1>
          <p className="hint">The platform host and every application on it — like a task manager.</p>
        </div>
        <div className="dash-controls">
          {data && (
            <span className="dash-updated">
              {paused ? "Paused" : "Live"} · updated {new Date(data.generated_at).toLocaleTimeString()}
            </span>
          )}
          <button type="button" onClick={() => setPaused((p) => !p)}>
            {paused ? "Resume" : "Pause"}
          </button>
          <button type="button" onClick={load}>
            Refresh
          </button>
        </div>
      </div>

      {error && <div className="error-banner" role="alert">{error}</div>}
      {!data && !error && <p className="hint">Loading…</p>}

      {data && (
        <>
          <section className="dash-tiles" aria-label="Platform host">
            <div className="dash-tile">
              <div className="dash-tile-head">
                <span>CPU</span>
                <strong>{host?.available ? pct(host.cpu_percent) : "—"}</strong>
              </div>
              {host?.available ? (
                <>
                  <Meter percent={host.cpu_percent} />
                  <p className="dash-sub">
                    {host.cpu_count} cores · load {host.load_1.toFixed(2)}
                  </p>
                  <Sparkline values={history.map((p) => p.cpu_percent)} max={100} label="CPU over the last 15 minutes" />
                </>
              ) : (
                <p className="dash-sub">Host readings unavailable</p>
              )}
            </div>

            <div className="dash-tile">
              <div className="dash-tile-head">
                <span>Memory</span>
                <strong>{host?.available ? pct(memPercent) : "—"}</strong>
              </div>
              {host?.available ? (
                <>
                  <Meter percent={memPercent} />
                  <p className="dash-sub">
                    {formatBytes(host.memory_used_bytes)} of {formatBytes(host.memory_total_bytes)} used
                  </p>
                  <Sparkline
                    values={history.map((p) => (p.memory_total_bytes ? (100 * p.memory_used_bytes) / p.memory_total_bytes : 0))}
                    max={100}
                    label="Memory over the last 15 minutes"
                  />
                </>
              ) : (
                <p className="dash-sub">Host readings unavailable</p>
              )}
            </div>

            <div className="dash-tile">
              <div className="dash-tile-head">
                <span>Traffic</span>
                <strong>{lastMinute ? lastMinute.requests.toLocaleString() : 0}</strong>
              </div>
              <p className="dash-sub">
                requests in the latest minute
                {lastMinute && lastMinute.errors > 0 ? ` · ${lastMinute.errors} errors` : ""}
              </p>
              <Sparkline values={traffic.map((p) => p.requests)} max={1} label="Requests per minute over the last 15 minutes" />
            </div>

            <div className="dash-tile">
              <div className="dash-tile-head">
                <span>Disk</span>
                <strong>{data.disk_used_percent != null ? pct(data.disk_used_percent) : "—"}</strong>
              </div>
              {data.disk_used_percent != null && <Meter percent={data.disk_used_percent} />}
              <p className="dash-sub">
                {host?.available ? `up ${formatUptime(host.uptime_seconds)}` : "where the platform keeps its data"}
              </p>
            </div>

            <div className="dash-tile">
              <div className="dash-tile-head">
                <span>Applications</span>
                <strong>{running.length}</strong>
              </div>
              <p className="dash-sub">
                running of {data.applications.length} · {instances} {instances === 1 ? "instance" : "instances"}
              </p>
            </div>
          </section>

          <section className="card">
            <div className="dash-table-head">
              <h2>Applications</h2>
              <input
                type="search"
                aria-label="Filter applications"
                placeholder="Filter by name, owner or department…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              />
            </div>
            <p className="hint">
              CPU and memory are each application's containers right now (100% CPU = one core). Requests, errors and
              latency cover the last {windowMin} minutes.
            </p>
            {apps.length === 0 ? (
              <p className="empty-state">{data.applications.length === 0 ? "No applications yet." : "No application matches."}</p>
            ) : (
              <div className="table-wrap">
                <table className="data-table dash-table">
                  <thead>
                    <tr>
                      {COLUMNS.map((c) => (
                        <th
                          key={c.key}
                          className={c.numeric ? "numeric" : undefined}
                          aria-sort={sort.key === c.key ? (sort.desc ? "descending" : "ascending") : "none"}
                        >
                          <button type="button" className="dash-sort" onClick={() => toggleSort(c.key)}>
                            {c.label}
                            {sort.key === c.key ? (sort.desc ? " ▾" : " ▴") : ""}
                          </button>
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {apps.map((a) => (
                      <AppRow key={a.id} app={a} cpuCapacity={cpuCapacity} memTotal={host?.memory_total_bytes ?? 0} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}

function AppRow({ app, cpuCapacity, memTotal }: { app: AdminApplication; cpuCapacity: number; memTotal: number }) {
  // Shaded like a task manager: how much of the whole host this one uses.
  const cpuShare = cpuCapacity > 0 ? (100 * app.cpu_percent) / cpuCapacity : 0;
  const memShare = memTotal > 0 ? (100 * app.memory_bytes) / memTotal : 0;
  const errorRate = app.requests > 0 ? (100 * app.errors) / app.requests : 0;
  return (
    <tr>
      <td>
        <Link to={`/applications/${app.id}`}>{app.name}</Link>
        <div className="dash-owner">
          {[app.owner_email, app.department, app.environments].filter(Boolean).join(" · ")}
        </div>
      </td>
      <td>
        <StatusBadge status={app.status} />
      </td>
      <td className="numeric">
        {app.instances === 0 && app.status === "running" ? (
          <span title="Scaled to zero: it starts again on the next request">0 · idle</span>
        ) : (
          app.instances
        )}
      </td>
      <td className={`numeric dash-heat dash-${level(cpuShare * 4)}`} style={{ ["--share" as string]: `${Math.min(100, cpuShare)}%` }}>
        {app.instances > 0 ? pct(app.cpu_percent) : "—"}
      </td>
      <td className={`numeric dash-heat dash-${level(memShare * 4)}`} style={{ ["--share" as string]: `${Math.min(100, memShare)}%` }}>
        {app.instances > 0 ? formatBytes(app.memory_bytes) : "—"}
      </td>
      <td className="numeric">{app.requests.toLocaleString()}</td>
      <td className={`numeric${errorRate >= 5 ? " dash-error-count" : ""}`}>{app.errors.toLocaleString()}</td>
      <td className="numeric">{app.requests > 0 ? `${Math.round(app.latency_ms_avg)} ms` : "—"}</td>
    </tr>
  );
}
