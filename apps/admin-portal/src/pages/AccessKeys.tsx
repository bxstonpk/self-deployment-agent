import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { createApiKey, listApiKeys, revokeApiKey } from "../api/client";
import { ApiError, type ApiKey } from "../api/types";

function when(iso: string | null): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

function keyState(key: ApiKey): { label: string; className: string } {
  if (key.revoked_at) return { label: "revoked", className: "badge badge-neutral" };
  if (!key.active) return { label: "expired", className: "badge badge-warning" };
  return { label: "active", className: "badge badge-success" };
}

// Every machine that can act as this employee: the keys Claude Code
// machines fetched through device approval, and any issued here by hand.
// Values are never listed — only the first characters, to tell them apart.
export function AccessKeys() {
  const { identity } = useIdentity();
  const [keys, setKeys] = useState<ApiKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [days, setDays] = useState(30);
  const [issued, setIssued] = useState<{ name: string; value: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!identity) return;
    try {
      setKeys((await listApiKeys(identity)).api_keys);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }, [identity]);

  useEffect(() => {
    void load();
  }, [load]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    if (!identity) return;
    setBusy(true);
    setError(null);
    try {
      const out = await createApiKey(identity, name.trim(), days);
      setIssued({ name: out.credential.name, value: out.api_key });
      setName("");
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onRevoke(key: ApiKey) {
    if (!identity) return;
    if (!window.confirm(`Revoke "${key.name}"? Whatever uses it stops working immediately.`)) return;
    try {
      await revokeApiKey(identity, key.id);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <h1>Access keys</h1>
      </div>
      <p className="hint">
        Each Claude Code machine signs in with its own key, which it gets when you approve its code on the{" "}
        <Link to="/device">Approve a machine</Link> page. Revoke a key the moment its machine is lost, shared or
        retired.
      </p>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {issued && (
        <div className="info-banner" role="status">
          <p>
            Key <strong>{issued.name}</strong> — copy it now, it will not be shown again:
          </p>
          <code data-testid="issued-key">{issued.value}</code>
          <p>
            <button onClick={() => setIssued(null)}>I've stored it</button>
          </p>
        </div>
      )}
      {keys === null ? (
        <p>Loading…</p>
      ) : keys.length === 0 ? (
        <p className="hint empty-state">No keys yet.</p>
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Key</th>
                <th>Status</th>
                <th>Created</th>
                <th>Last used</th>
                <th>Expires</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {keys.map((key) => {
                const state = keyState(key);
                return (
                  <tr key={key.id}>
                    <td>{key.name}</td>
                    <td>
                      <code>{key.prefix}…</code>
                    </td>
                    <td>
                      <span className={state.className}>{state.label}</span>
                    </td>
                    <td>{when(key.created_at)}</td>
                    <td>{when(key.last_used_at)}</td>
                    <td>{when(key.expires_at)}</td>
                    <td>
                      {state.label === "active" && (
                        <button className="button-danger" onClick={() => onRevoke(key)}>
                          Revoke
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <section className="card">
        <h2>Issue a key by hand</h2>
        <p className="hint">
          Only for a machine that can't run the sign-in itself. Claude Code machines should use{" "}
          <code>company-deploy login</code> or the <code>login</code> tool instead, so the key never passes through a
          screen or clipboard.
        </p>
        <form onSubmit={onCreate} className="toolbar">
          <label>
            Name
            <input required value={name} onChange={(e) => setName(e.target.value)} placeholder="build-server-01" />
          </label>
          <label>
            Expires in (days)
            <input type="number" min={1} max={90} value={days} onChange={(e) => setDays(Number(e.target.value))} />
          </label>
          <button type="submit" className="button-primary" disabled={busy}>
            Issue key
          </button>
        </form>
      </section>
    </div>
  );
}
