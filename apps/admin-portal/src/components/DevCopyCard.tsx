import { useCallback, useEffect, useState } from "react";
import { useIdentity } from "../context/IdentityContext";
import { getDevCopy, resetDevCopy, type DevCopy } from "../api/client";
import { ApiError } from "../api/types";

// An application's dev copy: its own application, database and address, to
// try changes on without approval. Shown only while there is one.
export function DevCopyCard({ applicationId }: { applicationId: string }) {
  const { identity } = useIdentity();
  const [copy, setCopy] = useState<DevCopy | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    if (!identity) return;
    getDevCopy(identity, applicationId)
      .then(setCopy)
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)));
  }, [identity, applicationId]);

  useEffect(() => {
    load();
  }, [load]);

  if (!copy && !error) return null;

  async function handleReset() {
    if (!identity) return;
    setBusy(true);
    setError(null);
    try {
      await resetDevCopy(identity, applicationId);
      setCopy(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  }

  return (
    <section className="card">
      <h2>Dev copy</h2>
      {copy && (
        <>
          <p>
            <a href={copy.url} target="_blank" rel="noreferrer">
              {copy.name}
            </a>{" "}
            — its own database; deploying to it doesn't wait for approval, and its data stays between deployments.
          </p>
          {copy.test_data ? (
            <div className="info-banner" role="note">
              <strong>Test data Claude added:</strong>
              <pre className="code-block">{copy.test_data_note}</pre>
            </div>
          ) : (
            <p className="hint">Claude hasn't recorded adding any test data.</p>
          )}
          {confirming ? (
            <p>
              This removes the dev copy's code and all its data.{" "}
              <button type="button" className="button-danger" disabled={busy} onClick={handleReset}>
                {busy ? "Removing…" : "Remove it"}
              </button>{" "}
              <button type="button" onClick={() => setConfirming(false)}>
                Cancel
              </button>
            </p>
          ) : (
            <button type="button" onClick={() => setConfirming(true)}>
              Reset dev copy
            </button>
          )}
        </>
      )}
      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}
    </section>
  );
}
