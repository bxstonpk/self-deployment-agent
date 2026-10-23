import { useEffect, useState } from "react";
import { Navigate, Route, Routes, Link, NavLink, useLocation } from "react-router-dom";
import { useIdentity } from "./context/IdentityContext";
import { getMe, listNotifications } from "./api/client";
import { SignIn } from "./pages/SignIn";
import { Register } from "./pages/Register";
import { DeviceApproval } from "./pages/DeviceApproval";
import { AccessKeys } from "./pages/AccessKeys";
import { ApplicationList } from "./pages/ApplicationList";
import { ApplicationDetail } from "./pages/ApplicationDetail";
import { RegisterApplication } from "./pages/RegisterApplication";
import { AuditLog } from "./pages/AuditLog";
import { Notifications } from "./pages/Notifications";
import { Reports } from "./pages/Reports";
import { Dashboard } from "./pages/Dashboard";
import { Departments } from "./pages/Departments";
import "./App.css";

// Polled rather than pushed — there's no websocket/SSE channel anywhere in
// this platform, and a 20s staleness window is a reasonable trade-off for
// an internal tool's header badge over building one just for this.
const UNREAD_POLL_INTERVAL_MS = 20_000;

function RequireIdentity({ children }: { children: React.ReactNode }) {
  const { identity } = useIdentity();
  const location = useLocation();
  // Come back here after signing in — what makes a machine's approval
  // link (/device?code=...) work for someone not signed in yet.
  if (!identity) {
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/sign-in?next=${next}`} replace />;
  }
  return <>{children}</>;
}

function Layout({ children }: { children: React.ReactNode }) {
  const { identity, signOut } = useIdentity();
  const [unreadCount, setUnreadCount] = useState(0);
  // Whether to offer the administrators' dashboard. Only a hint for the
  // menu: the Platform API refuses the dashboard to anyone else anyway.
  // Keyed by the session it was answered for, so signing out or in as
  // someone else never shows the previous account's menu.
  const [adminSession, setAdminSession] = useState<string | null>(null);
  const isAdmin = identity !== null && adminSession === identity.token;

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    getMe(identity)
      .then((me) => {
        if (!cancelled) setAdminSession(me.is_admin ? identity.token : null);
      })
      .catch(() => {
        // No menu entry is the safe answer when we can't tell.
      });
    return () => {
      cancelled = true;
    };
  }, [identity]);

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    const poll = () => {
      listNotifications(identity, true)
        .then((data) => {
          if (!cancelled) setUnreadCount(data.notifications?.length ?? 0);
        })
        .catch(() => {
          // A stale/missing badge count isn't worth surfacing an error banner for.
        });
    };
    poll();
    const interval = setInterval(poll, UNREAD_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [identity]);

  // Signed out: the sign-in and register pages sit alone on the brand
  // background, with no navigation to offer yet.
  if (!identity) {
    return (
      <div className="auth-shell">
        <header className="auth-brand">
          <Link to="/applications" className="app-title">
            <BrandMark />
            <span>Company Deployment Platform</span>
          </Link>
        </header>
        <main className="auth-main">{children}</main>
      </div>
    );
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <Link to="/applications" className="app-title">
            <BrandMark />
            <span>Company Deployment Platform</span>
          </Link>
        </div>
        <nav className="side-nav">
          {isAdmin && (
            <NavLink to="/dashboard" className="nav-link">
              <NavIcon d={ICONS.dashboard} />
              <span>Dashboard</span>
            </NavLink>
          )}
          <NavLink to="/applications" className="nav-link">
            <NavIcon d={ICONS.applications} />
            <span>Applications</span>
          </NavLink>
          <NavLink to="/departments" className="nav-link">
            <NavIcon d={ICONS.departments} />
            <span>Departments</span>
          </NavLink>
          <NavLink to="/notifications" className="nav-link">
            <NavIcon d={ICONS.notifications} />
            <span>
              Notifications
              {unreadCount > 0 && <span className="unread-count-badge">{unreadCount}</span>}
            </span>
          </NavLink>
          <NavLink to="/reports" className="nav-link">
            <NavIcon d={ICONS.reports} />
            <span>Reports</span>
          </NavLink>
          <NavLink to="/audit-log" className="nav-link">
            <NavIcon d={ICONS.auditLog} />
            <span>Audit Log</span>
          </NavLink>
          <NavLink to="/access-keys" className="nav-link">
            <NavIcon d={ICONS.accessKeys} />
            <span>Access keys</span>
          </NavLink>
        </nav>
        <div className="sidebar-user">
          <span className="user-avatar" aria-hidden="true">
            {(identity.name || identity.email).trim().charAt(0).toUpperCase()}
          </span>
          <span className="user-text">
            {identity.name && <span className="user-name">{identity.name}</span>}
            <span className="user-email">{identity.email}</span>
          </span>
          <button className="button-signout" onClick={signOut}>
            Sign out
          </button>
        </div>
      </aside>
      <main className="app-main">{children}</main>
    </div>
  );
}

// Decorative only: every icon sits next to its own text label.
function BrandMark() {
  return (
    <span className="brand-mark brand-mark-text" aria-hidden="true">
      STI
    </span>
  );
}

const ICONS = {
  dashboard: "M3 12h4l3-8 4 16 3-8h4",
  departments: "M3 21h18M5 21V7l7-4 7 4v14M9 9h1M14 9h1M9 13h1M14 13h1M9 17h6",
  applications: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
  notifications: "M6 8a6 6 0 1 1 12 0c0 7 3 9 3 9H3s3-2 3-9M10.3 21a1.94 1.94 0 0 0 3.4 0",
  reports: "M4 20V10M10 20V4M16 20v-7M22 20H2",
  auditLog: "M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M8 13h8M8 17h8",
  accessKeys: "M15.5 7.5a3.5 3.5 0 1 1-7 0 3.5 3.5 0 0 1 7 0zM12 11v10M12 17h3M12 20h2",
};

function NavIcon({ d }: { d: string }) {
  return (
    <svg className="nav-icon" aria-hidden="true" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d={d} />
    </svg>
  );
}

function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Navigate to="/applications" replace />} />
        <Route path="/sign-in" element={<SignIn />} />
        <Route
          path="/departments"
          element={
            <RequireIdentity>
              <Departments />
            </RequireIdentity>
          }
        />
        <Route
          path="/dashboard"
          element={
            <RequireIdentity>
              <Dashboard />
            </RequireIdentity>
          }
        />
        <Route path="/register" element={<Register />} />
        <Route
          path="/device"
          element={
            <RequireIdentity>
              <DeviceApproval />
            </RequireIdentity>
          }
        />
        <Route
          path="/access-keys"
          element={
            <RequireIdentity>
              <AccessKeys />
            </RequireIdentity>
          }
        />
        <Route
          path="/applications"
          element={
            <RequireIdentity>
              <ApplicationList />
            </RequireIdentity>
          }
        />
        <Route
          path="/applications/new"
          element={
            <RequireIdentity>
              <RegisterApplication />
            </RequireIdentity>
          }
        />
        <Route
          path="/applications/:id"
          element={
            <RequireIdentity>
              <ApplicationDetail />
            </RequireIdentity>
          }
        />
        <Route
          path="/audit-log"
          element={
            <RequireIdentity>
              <AuditLog />
            </RequireIdentity>
          }
        />
        <Route
          path="/reports"
          element={
            <RequireIdentity>
              <Reports />
            </RequireIdentity>
          }
        />
        <Route
          path="/notifications"
          element={
            <RequireIdentity>
              <Notifications />
            </RequireIdentity>
          }
        />
      </Routes>
    </Layout>
  );
}

export default App;
