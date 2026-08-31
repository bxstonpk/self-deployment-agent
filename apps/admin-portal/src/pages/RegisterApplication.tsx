import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { listDepartments, registerApplication } from "../api/client";
import type { Department } from "../api/types";
import { ApiError } from "../api/types";
import { useIdentity } from "../context/IdentityContext";

export function RegisterApplication() {
  const { identity } = useIdentity();
  const navigate = useNavigate();
  const [departments, setDepartments] = useState<Department[]>([]);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [departmentId, setDepartmentId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!identity) return;
    listDepartments(identity)
      .then((depts) => {
        setDepartments(depts);
        if (depts.length > 0) setDepartmentId(depts[0].id);
      })
      .catch((err: ApiError) => setError(err.message));
  }, [identity]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!identity || !departmentId) return;
    setSubmitting(true);
    setError(null);
    try {
      const app = await registerApplication(identity, {
        name: name.trim(),
        description: description.trim(),
        owning_department_id: departmentId,
      });
      navigate(`/applications/${app.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  }

  if (!identity) return null;

  return (
    <div className="page page-narrow">
      <h1>Register application</h1>
      <p className="hint">
        Creates the application in <code>draft</code> — you'll write and validate its{" "}
        <code>deployment.yaml</code> on the next screen.
      </p>
      {error && <div className="error-banner">{error}</div>}
      <form onSubmit={handleSubmit}>
        <label>
          Name
          <input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="overtime"
            pattern="[a-z]([a-z0-9\-]{0,61}[a-z0-9])?"
            title="Lowercase letters, digits, hyphens; must start with a letter (DNS-label rule)"
          />
        </label>
        <label>
          Description
          <input value={description} onChange={(e) => setDescription(e.target.value)} placeholder="HR overtime tracker" />
        </label>
        <label>
          Owning department
          <select value={departmentId} onChange={(e) => setDepartmentId(e.target.value)} required>
            {departments.length === 0 && <option value="">No departments yet</option>}
            {departments.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </label>
        <button type="submit" disabled={submitting || !departmentId}>
          {submitting ? "Registering…" : "Register"}
        </button>
      </form>
    </div>
  );
}
