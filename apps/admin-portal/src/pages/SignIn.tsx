import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";

export function SignIn() {
  const { signIn } = useIdentity();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [department, setDepartment] = useState("");

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!email.trim()) return;
    signIn({ email: email.trim(), name: name.trim() || undefined, department: department.trim() || undefined });
    navigate("/applications");
  }

  return (
    <div className="page page-narrow">
      <h1>Sign in</h1>
      <p className="hint">
        Dev-mode identity only — mirrors the Platform API's own temporary
        header-based auth stub (see DEC-001). Any email works; the
        Platform API creates the user/department on first use.
      </p>
      <form onSubmit={handleSubmit}>
        <label>
          Email
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="alice@example.com"
          />
        </label>
        <label>
          Name (optional)
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Alice Employee" />
        </label>
        <label>
          Department (optional)
          <input
            value={department}
            onChange={(e) => setDepartment(e.target.value)}
            placeholder="Engineering"
          />
        </label>
        <button type="submit">Sign in</button>
      </form>
    </div>
  );
}
