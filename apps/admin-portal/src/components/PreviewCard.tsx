import { useCallback, useEffect, useState } from "react";
import { useIdentity } from "../context/IdentityContext";
import { getPreview, stopPreview, type Preview } from "../api/client";
import { ApiError } from "../api/types";

// An application's preview: a short-lived copy with its own empty database,
// made by Claude's preview_application. Shown only while there is one.
export function PreviewCard({ applicationId }: { applicationId: string }) {
  const { identity } = useIdentity();
  const [preview, setPreview] = useState<Preview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [stopping, setStopping] = useState(false);

  const load = useCallback(() => {
    if (!identity) return;
    getPreview(identity, applicationId)
      .then(setPreview)
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)));
  }, [identity, applicationId]);

  useEffect(() => {
    load();
  }, [load]);

  if (!preview && !error) return null;

  async function handleStop() {
    if (!identity) return;
    setStopping(true);
    setError(null);
    try {
      await stopPreview(identity, applicationId);
      setPreview(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setStopping(false);
    }
  }

  return (
    <section className="card">
      <h2>Preview</h2>
      {preview && (
        <>
          <p>
            <a href={preview.url} target="_blank" rel="noreferrer">
              {preview.name}
            </a>{" "}
            — until {new Date(preview.expires_at).toLocaleString()}
          </p>
          <p className="hint">
            A separate copy with its own empty database: this application and its data aren't touched. It is removed
            when it expires.
          </p>
          <button type="button" className="button-danger" disabled={stopping} onClick={handleStop}>
            {stopping ? "Stopping…" : "Stop preview"}
          </button>
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
