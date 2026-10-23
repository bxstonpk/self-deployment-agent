import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { register } from "../api/client";
import { ApiError } from "../api/types";
import { safeNext } from "../navigation";

export function Register() {
  const { signIn } = useIdentity();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const [form, setForm] = useState({ email: "", fullName: "", department: "", password: "", confirm: "", code: "" });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form, [key]: e.target.value });

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (form.password !== form.confirm) {
      setError("The two passwords don't match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const session = await register({
        email: form.email.trim(),
        password: form.password,
        full_name: form.fullName.trim() || undefined,
        department: form.department.trim() || undefined,
        enrollment_code: form.code.trim(),
      });
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
      <h1>Create an account</h1>
      <p className="hint">
        You need the enrollment code IT gave you. If you used the platform before passwords existed, register with
        the same email — your applications stay yours.
      </p>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      <form onSubmit={handleSubmit}>
        <label>
          Email
          <input type="email" required autoComplete="username" value={form.email} onChange={set("email")} />
        </label>
        <label>
          Full name
          <input autoComplete="name" value={form.fullName} onChange={set("fullName")} />
        </label>
        <label>
          Department
          <input value={form.department} onChange={set("department")} placeholder="Engineering" />
        </label>
        <label>
          Password (10–72 characters)
          <input
            type="password"
            required
            minLength={10}
            maxLength={72}
            autoComplete="new-password"
            value={form.password}
            onChange={set("password")}
          />
        </label>
        <label>
          Confirm password
          <input type="password" required autoComplete="new-password" value={form.confirm} onChange={set("confirm")} />
        </label>
        <label>
          Enrollment code
          <input required autoComplete="off" value={form.code} onChange={set("code")} />
        </label>
        <button type="submit" className="button-primary" disabled={busy}>
          {busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="hint">
        Already have an account? <Link to={`/sign-in?next=${encodeURIComponent(next)}`}>Sign in</Link>.
      </p>
    </div>
  );
}
