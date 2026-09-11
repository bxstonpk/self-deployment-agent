// Types mirror services/platform-api's actual JSON response shapes exactly
// (checked against the handler source, not assumed from docs) — including
// its two real inconsistencies, both left in place deliberately rather than
// silently normalized here, so a reader comparing this file to a raw
// network response isn't confused by a mismatch:
//
// 1. Most responses (applications, deployments, builds) use snake_case,
//    hand-written response DTOs with explicit `json` tags.
// 2. GET /departments and GET /supported-stacks return raw Go structs with
//    NO json tags, so Go's default (PascalCase) field names leak through —
//    see DepartmentRaw/SupportedStackRaw below and client.ts's normalizers.

export type LifecycleStatus =
  | "draft"
  | "validated"
  | "build"
  | "deploying"
  | "running"
  | "suspended"
  | "rolled_back"
  | "failed"
  | "archived"
  | "deleted";

export type DeploymentStatus =
  | "scanning"
  | "pending_approval"
  | "deploying"
  | "health_check"
  | "running"
  | "failed"
  | "rejected"
  | "superseded"
  | "suspended"
  | "archived";

export type BuildStatus = "queued" | "in_progress" | "succeeded" | "failed";

export interface Application {
  id: string;
  name: string;
  description: string;
  owning_department_id: string;
  created_by: string;
  lifecycle_status: LifecycleStatus;
  deployment_yaml_draft?: string;
  created_at: string;
  updated_at: string;
  validated_at?: string;
}

// --- Ownership (Module E) ---

export type OwnershipRole = "primary" | "secondary" | "technical";

export interface ApplicationOwner {
  user_id: string;
  ownership_role: OwnershipRole;
  status: "active" | "revoked";
  assigned_at: string;
}

export type TransferStatus = "pending" | "accepted" | "expired";

export interface OwnershipTransfer {
  id: string;
  application_id: string;
  from_user_id: string;
  to_user_id: string;
  status: TransferStatus;
  initiated_at: string;
  expires_at: string;
  resolved_at?: string;
}

// --- Secrets (Module O) ---

// Metadata only. platform-api has no endpoint that returns a secret's
// value (FR-070), so there is deliberately no `value` field here for
// anything to ever populate.
export type SecretManagedBy = "employee" | "platform";

export interface SecretMetadata {
  name: string;
  managed_by: SecretManagedBy;
  version: number;
  updated_by: string | null;
  created_at: string;
  updated_at: string;
}

// What rotating a platform-generated secret returns (FR-068): the new
// version and whether running instances were restarted onto it — never the
// value.
export interface RotationResult {
  secret: SecretMetadata;
  restarted: boolean;
  note: string;
}

export interface RunningContainer {
  container_id: string;
  host_port: number;
  url: string;
}

export interface ScanReport {
  image_ref: string;
  passed: boolean;
  critical_count: number;
  high_count: number;
  top_findings?: { severity: string; vulnerability_id: string; package: string; title: string }[];
}

export interface Deployment {
  id: string;
  application_id: string;
  build_id: string;
  environment: "dev" | "production";
  status: DeploymentStatus;
  scan_passed?: boolean;
  scan_critical_count?: number;
  scan_high_count?: number;
  scan_reports?: Record<string, ScanReport>;
  rejection_reason?: string;
  failure_reason?: string;
  containers?: Record<string, RunningContainer>;
  created_at: string;
  updated_at: string;
  completed_at?: string;
}

export interface Build {
  id: string;
  application_id: string;
  status: BuildStatus;
  error_category?: "source" | "platform";
  error_detail?: string;
  image_refs?: Record<string, string>;
  started_at: string;
  completed_at?: string;
}

export interface ValidationCheck {
  name: string;
  status: "passed" | "failed" | "skipped";
  details?: string[];
}

export interface ValidationReport {
  valid: boolean;
  checks: ValidationCheck[];
}

export interface ScaleEvent {
  service_name: string;
  direction: "cold_start" | "scale_to_zero" | string;
  trigger_reason: string;
  occurred_at: string;
}

// --- Raw shapes for the two endpoints with no json tags (see module doc) ---

export interface DepartmentRaw {
  ID: string;
  Name: string;
  CostCenterCode: string;
  Status: string;
  CreatedAt: string;
}

export interface Department {
  id: string;
  name: string;
  cost_center_code: string;
  status: string;
}

export interface SupportedStackRaw {
  ID: string;
  Kind: "frontend" | "backend" | "database" | "cache";
  Name: string;
  Status: string;
}

export interface SupportedStack {
  id: string;
  kind: SupportedStackRaw["Kind"];
  name: string;
  status: string;
}

// --- Audit Log (Module W) ---

export type AuditOutcome = "success" | "failure";

export interface AuditEntry {
  id: string;
  seq: number;
  occurred_at: string;
  actor_user_id: string;
  action: string;
  resource_type: string;
  resource_id: string;
  outcome: AuditOutcome;
  detail: string;
  entry_hash: string;
}

export interface AuditQueryParams {
  actorUserId?: string;
  resourceType?: string;
  resourceId?: string;
  action?: string;
  from?: string; // RFC3339
  to?: string; // RFC3339
  limit?: number;
}

// --- Notifications (Module X) ---

export type NotificationCategory = "deployment_status" | "approval_request";

export interface Notification {
  id: string;
  category: NotificationCategory;
  title: string;
  detail: string;
  resource_type: string;
  resource_id: string;
  read_at: string | null;
  created_at: string;
}

// --- Reporting (Module AB) ---

export interface InventoryRow {
  application_id: string;
  name: string;
  department_id: string;
  department_name: string;
  lifecycle_status: LifecycleStatus;
  owner_user_ids: string[];
  runtimes: string[];
  environment: string;
}

export interface OutcomeCounts {
  succeeded: number;
  failed: number;
  rolled_back: number;
}

export interface DeploymentActivityReport {
  from: string;
  to: string;
  // Present only when the requested range starts before any data this
  // platform actually holds (FR-128's exception flow).
  available_from?: string;
  total: OutcomeCounts;
  by_environment: Record<string, OutcomeCounts>;
  by_department: Record<string, OutcomeCounts>;
}

// --- API error envelope ---

export interface ApiErrorBody {
  error: { code: string; message: string };
}

export class ApiError extends Error {
  code: string;
  status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}
