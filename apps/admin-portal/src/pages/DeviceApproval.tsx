import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { useIdentity } from "../context/IdentityContext";
import { decideDevice, getPendingDevice } from "../api/client";
import { ApiError, type PendingDevice } from "../api/types";

// Where a machine's sign-in request is approved. Claude Code (or
// `company-deploy login`) shows the employee a code and this page's
// address; approving gives that machine an API key that acts as the
// signed-in employee. The key goes straight to the machine — it's never
// shown here.
export function DeviceApproval() {
  const { identity } = useIdentity();
  const [params, setParams] = useSearchParams();
  const [code, setCode] = useState(params.get("code") ?? "");
  const [device, setDevice] = useState<PendingDevice | null>(null);
  const [result, setResult] = useState<"approved" | "denied" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function lookUp(userCode: string) {
    if (!identity || !userCode.trim()) return;
    setBusy(true);
    setError(null);
    setDevice(null);
    setResult(null);
    try {
      setDevice((await getPendingDevice(identity, userCode.trim())).device);
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 404
          ? "No machine is waiting with that code. It may have expired (codes last 10 minutes) or already been used."
          : err instanceof ApiError
            ? err.message
            : String(err),
      );
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    const fromLink = params.get("code");
    if (fromLink) void lookUp(fromLink);
    // Only on arrival: later lookups are explicit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setParams(code.trim() ? { code: code.trim() } : {});
    void lookUp(code);
  }

  async function decide(approve: boolean) {
    if (!identity || !device) return;
    setBusy(true);
    setError(null);
    try {
      await decideDevice(identity, device.user_code, approve);
      setResult(approve ? "approved" : "denied");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page page-narrow">
      <h1>Approve a machine</h1>
      <p className="hint">
        Approve only a request you started yourself, on a machine you're using now. The machine gets an API key that
        acts as you ({identity?.email}) until it expires or you revoke it under <strong>Access keys</strong>.
      </p>
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
      {result === "approved" && (
        <div className="info-banner" role="status">
          Approved. The machine collects its key within a few seconds — go back to it.
        </div>
      )}
      {result === "denied" && (
        <div className="info-banner" role="status">
          Denied. The machine gets no key.
        </div>
      )}
      {!result && (
        <form onSubmit={onSubmit}>
          <label>
            Code shown on the machine
            <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="XXXX-XXXX" autoComplete="off" />
          </label>
          <button type="submit" disabled={busy}>
            Look up
          </button>
        </form>
      )}
      {device && !result && (
        <div className="card">
          <p>
            <strong>{device.client_name}</strong> is asking to sign in as you.
          </p>
          <p>
            Code: <code>{device.user_code}</code> — check it matches the one on that machine exactly.
          </p>
          <p className="hint">Requested {new Date(device.created_at).toLocaleString()}.</p>
          <div className="action-row">
            <button className="button-primary" disabled={busy} onClick={() => decide(true)}>
              Approve
            </button>
            <button className="button-danger" disabled={busy} onClick={() => decide(false)}>
              Deny
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
