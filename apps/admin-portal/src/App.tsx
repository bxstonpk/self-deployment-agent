import { Navigate, Route, Routes, Link } from "react-router-dom";
import { useIdentity } from "./context/IdentityContext";
import { SignIn } from "./pages/SignIn";
import { ApplicationList } from "./pages/ApplicationList";
import { ApplicationDetail } from "./pages/ApplicationDetail";
import { RegisterApplication } from "./pages/RegisterApplication";
import "./App.css";

function RequireIdentity({ children }: { children: React.ReactNode }) {
  const { identity } = useIdentity();
  if (!identity) return <Navigate to="/sign-in" replace />;
  return <>{children}</>;
}

function Layout({ children }: { children: React.ReactNode }) {
  const { identity, signOut } = useIdentity();
  return (
    <div className="app-shell">
      <header className="app-header">
        <Link to="/applications" className="app-title">
          Company Deployment Platform
        </Link>
        {identity && (
          <div className="identity-bar">
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
      </Routes>
    </Layout>
  );
}

export default App;
