// Reporting implements a scoped slice of Module AB
// (docs/02_Functional_Requirements.md): FR-127 (Application Inventory
// Report) and FR-128 (Deployment Activity Report). FR-129 (Resource
// Utilization Report) is a documented gap — it reports consumption
// "against quota, building on the real-time usage visibility of Module M",
// and Module M (Resource Management) doesn't exist: there is no quota to
// report against and nothing tracking allocation, so any number this
// produced would be invented rather than measured.
//
// Scope adaptation: both reports are scoped to applications the caller
// owns. FR-127's main flow describes a platform-wide report for a
// "Management/Auditor, Platform Administrator" holding a reporting-access
// role; no such role exists anywhere in this platform (blocked on
// DEC-002), so by FR-127's own exception flow ("requester's scope exceeds
// their authorization -> scope is limited to what they are authorized to
// see, not rejected outright") every caller today falls into its
// alternative flow: "Application Owner views a scoped inventory limited to
// applications they own". Treating everyone as an unprivileged owner is
// the safer reading than treating everyone as a de-facto Auditor.
package domain

import "time"

// InventoryRow implements FR-127's per-application output: "their
// department, owner, lifecycle state, stack, and environment".
type InventoryRow struct {
	ApplicationID   string
	Name            string
	DepartmentID    string
	DepartmentName  string
	LifecycleStatus LifecycleStatus
	OwnerUserIDs    []string
	// Runtimes is FR-127's "stack", read from the application's current
	// deployment.yaml draft. Empty when there's no draft yet or it no
	// longer parses — reported as empty rather than guessed.
	Runtimes []string
	// Environment is the most recent deployment's target environment, or
	// "" for an application that has never been deployed.
	Environment string
	CreatedAt   time.Time
}

// DeploymentOutcomeCounts implements FR-128's "counts by outcome
// (succeeded/failed/rolled back)". The three are mutually exclusive per
// deployment attempt: a rollback-originated attempt counts as RolledBack
// whether or not it itself succeeded, matching FR-128's own flat
// three-way framing rather than nesting success/failure inside it.
type DeploymentOutcomeCounts struct {
	Succeeded  int
	Failed     int
	RolledBack int
}

// DeploymentActivityReport implements FR-128's summarized, management-
// facing view — deliberately "derived from, but distinct from, the raw
// audit log (Module W)", per its business rule.
type DeploymentActivityReport struct {
	From time.Time
	To   time.Time
	// AvailableFrom implements FR-128's exception flow: when the requested
	// range starts before the earliest data this platform actually holds,
	// the report says so rather than silently under-reporting. Nil when
	// the requested range is fully covered.
	AvailableFrom *time.Time
	Total         DeploymentOutcomeCounts
	// ByEnvironment and ByDepartment are FR-128's two required breakdowns
	// ("by department, and by environment"), keyed by environment name and
	// department name respectively.
	ByEnvironment map[string]DeploymentOutcomeCounts
	ByDepartment  map[string]DeploymentOutcomeCounts
}
