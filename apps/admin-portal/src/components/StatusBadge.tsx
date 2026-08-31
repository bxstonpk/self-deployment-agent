const STATUS_CLASS: Record<string, string> = {
  draft: "badge-neutral",
  validated: "badge-neutral",
  build: "badge-info",
  deploying: "badge-info",
  running: "badge-success",
  suspended: "badge-warning",
  rolled_back: "badge-info",
  failed: "badge-danger",
  rejected: "badge-danger",
  archived: "badge-neutral",
  deleted: "badge-neutral",
  scanning: "badge-info",
  pending_approval: "badge-warning",
  health_check: "badge-info",
  superseded: "badge-neutral",
};

export function StatusBadge({ status }: { status: string }) {
  const cls = STATUS_CLASS[status] ?? "badge-neutral";
  return <span className={`badge ${cls}`}>{status}</span>;
}
