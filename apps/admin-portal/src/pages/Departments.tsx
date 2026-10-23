import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useIdentity } from "../context/IdentityContext";
import { createDepartment, listDepartmentSummaries, type DepartmentSummary } from "../api/client";
import { ApiError } from "../api/types";

// Every department, with how many people and applications belong to it,
// and a way to add one. There is no fixed list: a department is also added
// when someone registers under a new one, or registers an application for
// one that isn't here yet.
export function Departments() {
  const { identity } = useIdentity();
  const [departments, setDepartments] = useState<DepartmentSummary[] | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    if (!identity) return;
    listDepartmentSummaries(identity)
      .then((d) => setDepartments(d.departments))
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)));
  }, [identity]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleAdd(e: FormEvent) {
    e.preventDefault();
    if (!identity || !name.trim()) return;
    setSaving(true);
    setError(null);
    setNotice(null);
    const existed = (departments ?? []).some((d) => d.name.toLowerCase() === name.trim().replace(/\s+/g, " ").toLowerCase());
    try {
      const d = await createDepartment(identity, name);
      setNotice(existed ? `${d.name} already exists.` : `Added ${d.name}.`);
      setName("");
      load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <h1>Departments</h1>
      </div>
      <p className="hint">
        Applications and people belong to a department. Add one here, or it's added the first time someone registers
        under it or registers an application for it.
      </p>

      <section className="card">
        <h2>Add a department</h2>
        <form onSubmit={handleAdd} className="department-form">
          <label>
            Department name
            <input
              required
              maxLength={80}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Human Resources"
            />
          </label>
          <button type="submit" className="button-primary" disabled={saving || !name.trim()}>
            {saving ? "Adding…" : "Add department"}
          </button>
        </form>
        {notice && <div className="info-banner" role="status">{notice}</div>}
      </section>

      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}

      <section className="card">
        <h2>All departments</h2>
        {departments === null ? (
          <p className="hint">Loading…</p>
        ) : departments.length === 0 ? (
          <p className="empty-state">No departments yet.</p>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Department</th>
                  <th className="numeric">People</th>
                  <th className="numeric">Applications</th>
                  <th>Added</th>
                </tr>
              </thead>
              <tbody>
                {departments.map((d) => (
                  <tr key={d.id}>
                    <td>{d.name}</td>
                    <td className="numeric">{d.users}</td>
                    <td className="numeric">{d.applications}</td>
                    <td>{new Date(d.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
