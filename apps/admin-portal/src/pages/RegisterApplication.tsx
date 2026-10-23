import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { createDepartment, getMe, listDepartments, registerApplication } from "../api/client";
import type { Department } from "../api/types";
import { ApiError } from "../api/types";
import { useIdentity } from "../context/IdentityContext";

// The select's value for "a department that isn't in the list yet".
const NEW_DEPARTMENT = "__new__";

export function RegisterApplication() {
  const { identity } = useIdentity();
  const navigate = useNavigate();
  const [departments, setDepartments] = useState<Department[]>([]);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [departmentId, setDepartmentId] = useState("");
  const [newDepartment, setNewDepartment] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    Promise.all([listDepartments(identity), getMe(identity).catch(() => null)])
      .then(([depts, me]) => {
        if (cancelled) return;
        setDepartments(depts);
        // The employee's own department first; any department can be chosen.
        const own = depts.find((d) => d.id === me?.user?.department_id);
        if (own) setDepartmentId(own.id);
        else if (depts.length > 0) setDepartmentId(depts[0].id);
        else setDepartmentId(NEW_DEPARTMENT);
      })
      .catch((err: ApiError) => {
        if (!cancelled) setError(err.message);
      });
    return () => {
      cancelled = true;
    };
  }, [identity]);

  const addingDepartment = departmentId === NEW_DEPARTMENT;
  const ready = addingDepartment ? newDepartment.trim() !== "" : departmentId !== "";

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!identity || !ready) return;
    setSubmitting(true);
    setError(null);
    try {
      let owningDepartmentId = departmentId;
      if (addingDepartment) {
        // Returns the existing department if one of that name (any case) exists.
        owningDepartmentId = (await createDepartment(identity, newDepartment)).id;
      }
      const app = await registerApplication(identity, {
        name: name.trim(),
        description: description.trim(),
        owning_department_id: owningDepartmentId,
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
            {departments.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
            <option value={NEW_DEPARTMENT}>+ New department…</option>
          </select>
        </label>
        {addingDepartment && (
          <label>
            New department name
            <input
              required
              maxLength={80}
              value={newDepartment}
              onChange={(e) => setNewDepartment(e.target.value)}
              placeholder="e.g. Human Resources"
            />
          </label>
        )}
        <button type="submit" disabled={submitting || !ready}>
          {submitting ? "Registering…" : "Register"}
        </button>
      </form>
    </div>
  );
}
