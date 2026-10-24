import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useIdentity } from "../context/IdentityContext";
import { decideApproval, getApproval, getApprovalSource, type ApprovalRequest } from "../api/client";
import { ApiError } from "../api/types";
import { decryptKeyFile, fingerprint, KeyFileError, signDecision } from "../approval/keyfile";
import { asText, readTarGz, sha256Hex, type SourceFile } from "../approval/tarball";

type Tab = "review" | "files" | "tests" | "signed";

// One deployment waiting for approval: everything to review, and signing.
// The source shown is checked against the SHA-256 that the signature covers,
// so what's read here is exactly what goes live if approved.
export function ApprovalDetail() {
  const { deploymentId = "" } = useParams();
  const { identity } = useIdentity();
  const [req, setReq] = useState<ApprovalRequest | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [files, setFiles] = useState<SourceFile[] | null>(null);
  const [archive, setArchive] = useState<Uint8Array | null>(null);
  const [sourceState, setSourceState] = useState<"loading" | "verified" | "mismatch" | "none" | "error">("loading");
  const [tab, setTab] = useState<Tab>("review");

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    getApproval(identity, deploymentId)
      .then(async (r) => {
        if (cancelled) return;
        setReq(r);
        if (!r.source_sha256 || r.source_sha256 === "none") {
          setSourceState("none");
          return;
        }
        try {
          const bytes = await getApprovalSource(identity, deploymentId);
          if (cancelled) return;
          if (sha256Hex(bytes) !== r.source_sha256) {
            setSourceState("mismatch");
            return;
          }
          setArchive(bytes);
          setFiles(readTarGz(bytes).sort((a, b) => a.path.localeCompare(b.path)));
          setSourceState("verified");
        } catch {
          if (!cancelled) setSourceState("error");
        }
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof ApiError ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [identity, deploymentId]);

  if (error) {
    return (
      <div className="page">
        <p>
          <Link to="/approvals">← Approvals</Link>
        </p>
        <div className="error-banner" role="alert">
          {error}
        </div>
      </div>
    );
  }
  if (!req) return <div className="page"><p className="hint">Loading…</p></div>;

  const pendingDecision = req.status === "pending_approval";
  const scanFailed = Object.values(req.scans).some((s) => !s.passed);

  return (
    <div className="page approval">
      <p className="back-link">
        <Link to="/approvals">← Approvals</Link>
      </p>
      <div className="page-header">
        <h1>
          {req.application_name} <span className={`env-badge env-${req.environment}`}>{req.environment}</span>
        </h1>
      </div>
      <p className="hint">
        Requested by <b>{req.requested_by}</b> on {new Date(req.requested_at).toLocaleString()} · deployment{" "}
        <code>{req.deployment_id}</code> · status <b>{req.status}</b>
      </p>

      <section className="approval-facts">
        <div className={`fact fact-${sourceState === "verified" ? "ok" : sourceState === "mismatch" ? "bad" : "warn"}`}>
          <span>Source</span>
          <strong>
            {sourceState === "verified" && `${files?.length ?? 0} files, verified`}
            {sourceState === "loading" && "Downloading…"}
            {sourceState === "mismatch" && "Doesn't match what you'd sign"}
            {sourceState === "none" && "Not kept for this build"}
            {sourceState === "error" && "Couldn't download"}
          </strong>
          {req.source_sha256 && <code title="SHA-256 of the source archive">{req.source_sha256.slice(0, 16)}…</code>}
        </div>
        <div className={`fact fact-${req.review_document.trim() ? "ok" : "warn"}`}>
          <span>REVIEW.md</span>
          <strong>{req.review_document.trim() ? `${req.review_document.split("\n").length} lines` : "None attached"}</strong>
        </div>
        <div className={`fact fact-${req.test_report.trim() ? "ok" : "warn"}`}>
          <span>Tests</span>
          <strong>{req.test_report.trim() ? "Report attached" : "None attached"}</strong>
        </div>
        <div className={`fact fact-${scanFailed ? "bad" : "ok"}`}>
          <span>Image scans</span>
          <strong>{scanFailed ? "Not passed" : "Passed"}</strong>
        </div>
      </section>

      <div className="tabs" role="tablist">
        {(
          [
            ["review", "REVIEW.md"],
            ["files", `Files${files ? ` (${files.length})` : ""}`],
            ["tests", "Tests & scans"],
            ["signed", "What you sign"],
          ] as [Tab, string][]
        ).map(([t, label]) => (
          <button key={t} type="button" role="tab" aria-selected={tab === t} className={tab === t ? "tab active" : "tab"} onClick={() => setTab(t)}>
            {label}
          </button>
        ))}
      </div>

      <section className="card tab-panel" role="tabpanel">
        {tab === "review" &&
          (req.review_document.trim() ? (
            <div className="markdown">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{req.review_document}</ReactMarkdown>
            </div>
          ) : (
            <p className="empty-state">No REVIEW.md was attached to this deployment.</p>
          ))}
        {tab === "files" && <FileBrowser files={files} state={sourceState} archive={archive} name={req.application_name} />}
        {tab === "tests" && <TestsAndScans req={req} />}
        {tab === "signed" && (
          <>
            <p className="hint">
              Exactly what your signature covers: the deployment, the images, and the SHA-256 of the source, the test
              report and REVIEW.md. Change any of them and the signature no longer verifies.
            </p>
            <pre className="code-block">{req.signing_template}</pre>
          </>
        )}
      </section>

      {pendingDecision ? (
        <SignPanel req={req} sourceOk={sourceState === "verified" || sourceState === "none"} />
      ) : (
        <div className="info-banner" role="status">
          This deployment isn't waiting for a decision any more ({req.status}).
        </div>
      )}
    </div>
  );
}

function FileBrowser({
  files,
  state,
  archive,
  name,
}: {
  files: SourceFile[] | null;
  state: string;
  archive: Uint8Array | null;
  name: string;
}) {
  const [selected, setSelected] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const shown = useMemo(
    () => (files ?? []).filter((f) => !filter || f.path.toLowerCase().includes(filter.toLowerCase())),
    [files, filter],
  );
  const current = files?.find((f) => f.path === (selected ?? files[0]?.path)) ?? null;

  if (state === "mismatch") {
    return (
      <div className="error-banner" role="alert">
        The downloaded source doesn't match the SHA-256 the platform reported. Don't approve; check the platform.
      </div>
    );
  }
  if (!files) return <p className="hint">{state === "none" ? "No source was kept for this build." : "Loading the source…"}</p>;

  function download() {
    if (!archive) return;
    const url = URL.createObjectURL(new Blob([archive.slice().buffer], { type: "application/gzip" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `${name}-source.tar.gz`;
    a.click();
    URL.revokeObjectURL(url);
  }

  const text = current ? asText(current.data) : null;
  return (
    <div className="file-browser">
      <div className="file-list">
        <input
          type="search"
          aria-label="Filter files"
          placeholder="Filter files…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <ul>
          {shown.map((f) => (
            <li key={f.path}>
              <button
                type="button"
                className={current?.path === f.path ? "file active" : "file"}
                onClick={() => setSelected(f.path)}
                title={f.path}
              >
                {f.path}
              </button>
            </li>
          ))}
        </ul>
        <button type="button" onClick={download}>
          Download all (.tar.gz)
        </button>
      </div>
      <div className="file-view">
        {current && (
          <>
            <div className="file-view-head">
              <code>{current.path}</code>
              <span>{current.data.length.toLocaleString()} bytes</span>
            </div>
            {text === null ? (
              <p className="hint">Binary file — not shown.</p>
            ) : (
              <pre className="code-view" aria-label={`Contents of ${current.path}`}>
                {text.split("\n").map((line, i) => (
                  <span key={i} className="code-line">
                    <span className="ln">{i + 1}</span>
                    {line + "\n"}
                  </span>
                ))}
              </pre>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function TestsAndScans({ req }: { req: ApprovalRequest }) {
  return (
    <>
      <h3>Test report</h3>
      {req.test_report.trim() ? <pre className="code-block">{req.test_report}</pre> : <p className="empty-state">No test report was attached.</p>}
      <h3>Images and scans</h3>
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>Service</th>
              <th>Image</th>
              <th>Scan</th>
              <th className="numeric">Critical</th>
              <th className="numeric">High</th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(req.images)
              .sort()
              .map(([svc, ref]) => {
                const scan = req.scans[svc];
                return (
                  <tr key={svc}>
                    <td>{svc}</td>
                    <td>
                      <code>{ref}</code>
                    </td>
                    <td>{scan?.passed ? "Passed" : "Not passed"}</td>
                    <td className="numeric">{scan?.critical ?? "?"}</td>
                    <td className="numeric">{scan?.high ?? "?"}</td>
                  </tr>
                );
              })}
          </tbody>
        </table>
      </div>
    </>
  );
}

function SignPanel({ req, sourceOk }: { req: ApprovalRequest; sourceOk: boolean }) {
  const { identity } = useIdentity();
  const [decision, setDecision] = useState<"approve" | "reject">("approve");
  const [reason, setReason] = useState("");
  const [keyText, setKeyText] = useState<string | null>(null);
  const [keyName, setKeyName] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{ status: string; signer: string } | null>(null);

  async function pickKey(file: File | undefined) {
    setError(null);
    if (!file) {
      setKeyText(null);
      setKeyName("");
      return;
    }
    // Read here, in this tab's memory only; never uploaded.
    setKeyText(await file.text());
    setKeyName(file.name);
  }

  async function handleSign(e: FormEvent) {
    e.preventDefault();
    if (!identity || !keyText) return;
    if (decision === "reject" && !reason.trim()) {
      setError("Say why you're rejecting it — the requester sees the reason.");
      return;
    }
    setBusy(true);
    setError(null);
    let seed: Uint8Array | null = null;
    try {
      seed = decryptKeyFile(keyText, passphrase);
      const signed = signDecision(seed, req.signing_template, decision, reason);
      const keyFingerprint = fingerprint(seed);
      const out = await decideApproval(identity, req.deployment_id, { decision, reason, ...signed });
      setDone({ status: out.deployment.status, signer: out.signer_fingerprint || keyFingerprint });
    } catch (err) {
      if (err instanceof KeyFileError) setError(err.message);
      else if (err instanceof ApiError) setError(err.message);
      else setError(String(err));
    } finally {
      seed?.fill(0);
      setPassphrase("");
      setBusy(false);
    }
  }

  if (done) {
    return (
      <div className="info-banner" role="status">
        Signed with {done.signer}. The deployment is now <b>{done.status}</b>.
      </div>
    );
  }

  return (
    <section className="card sign-panel">
      <h2>Your decision</h2>
      <form onSubmit={handleSign}>
        <div className="decision-choice" role="radiogroup" aria-label="Decision">
          <label>
            <input type="radio" name="decision" checked={decision === "approve"} onChange={() => setDecision("approve")} />
            Approve
          </label>
          <label>
            <input type="radio" name="decision" checked={decision === "reject"} onChange={() => setDecision("reject")} />
            Reject
          </label>
        </div>
        <label>
          Reason {decision === "reject" ? "(required)" : "(optional)"}
          <textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} maxLength={500} />
        </label>
        <label>
          Approver key file
          <input type="file" accept=".pem,application/x-pem-file,text/plain" onChange={(e) => pickKey(e.target.files?.[0])} />
        </label>
        <p className="hint">
          Your <code>approver_ed25519.pem</code> (from <code>company-deploy approver keygen</code>, usually in{" "}
          <code>%USERPROFILE%\.company-deploy\</code>). It is read and used in this browser tab only and never uploaded:
          only the signature is sent.
        </p>
        <label>
          Key passphrase
          <input type="password" autoComplete="off" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
        </label>
        {!sourceOk && decision === "approve" && (
          <div className="error-banner" role="alert">
            The source couldn't be verified, so it can't be approved from here. Reject it, or check with{" "}
            <code>company-deploy review</code>.
          </div>
        )}
        {error && (
          <div className="error-banner" role="alert">
            {error}
          </div>
        )}
        <button
          type="submit"
          className={decision === "approve" ? "button-primary" : "button-danger"}
          disabled={busy || !keyText || !passphrase || (decision === "approve" && !sourceOk)}
        >
          {busy ? "Signing…" : decision === "approve" ? "Sign and approve" : "Sign and reject"}
        </button>
        {keyName && <span className="hint key-picked"> {keyName}</span>}
      </form>
    </section>
  );
}
