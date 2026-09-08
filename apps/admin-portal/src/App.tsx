import { useEffect, useState } from "react";
import { Navigate, Route, Routes, Link } from "react-router-dom";
import { useIdentity } from "./context/IdentityContext";
import { listNotifications } from "./api/client";
import { SignIn } from "./pages/SignIn";
import { ApplicationList } from "./pages/ApplicationList";
import { ApplicationDetail } from "./pages/ApplicationDetail";
import { RegisterApplication } from "./pages/RegisterApplication";
import { AuditLog } from "./pages/AuditLog";
import { Notifications } from "./pages/Notifications";
import "./App.css";

// Polled rather than pushed — there's no websocket/SSE channel anywhere in
// this platform, and a 20s staleness window is a reasonable trade-off for
// an internal tool's header badge over building one just for this.
const UNREAD_POLL_INTERVAL_MS = 20_000;

function RequireIdentity({ children }: { children: React.ReactNode }) {
  const { identity } = useIdentity();
  if (!identity) return <Navigate to="/sign-in" replace />;
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
            <Link to="/audit-log">Audit Log</Link>
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
