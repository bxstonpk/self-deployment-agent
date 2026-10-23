import { useEffect, useState } from "react";
import { Navigate, Route, Routes, Link, useLocation } from "react-router-dom";
import { useIdentity } from "./context/IdentityContext";
import { listNotifications } from "./api/client";
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

  return (
    <div className="app-shell">
      <header className="app-header">
        <Link to="/applications" className="app-title">
          Company Deployment Platform
        </Link>
        {identity && (
          <div className="identity-bar">
            <Link to="/notifications">
              Notifications
              {unreadCount > 0 && <span className="unread-count-badge">{unreadCount}</span>}
            </Link>
            <Link to="/reports">Reports</Link>
            <Link to="/audit-log">Audit Log</Link>
            <Link to="/access-keys">Access keys</Link>
            <span>{identity.email}</span>
            <button onClick={signOut}>Sign out</button>
          </div>
        )}
      </header>
      <main>{children}</main>
    </div>
  );
}

function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Navigate to="/applications" replace />} />
        <Route path="/sign-in" element={<SignIn />} />
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
