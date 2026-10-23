import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { login } from "../api/client";
import { ApiError } from "../api/types";
import { safeNext } from "../navigation";

export function SignIn() {
  const { signIn } = useIdentity();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const session = await login(email.trim(), password);
      signIn({ email: session.user.email, name: session.user.full_name, token: session.token, expiresAt: session.expires_at });
      navigate(next, { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page page-narrow">
      <h1>Sign in</h1>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      <form onSubmit={handleSubmit}>
        <label>
          Email
          <input type="email" required autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label>
          Password
          <input
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <button type="submit" className="button-primary" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
      <p className="hint">
        No account yet? <Link to={`/register?next=${encodeURIComponent(next)}`}>Create one</Link> with the enrollment
        code from IT.
      </p>
    </div>
  );
}
