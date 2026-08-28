package domain

// DeploymentYAML mirrors the application-level contract schema from
// docs/promt.md Section 5 / docs/02_Functional_Requirements.md Module G
// (FR-023). `yaml:",strict"`-style unknown-field rejection is applied by the
// caller (via yaml.Decoder.KnownFields), not here — this struct is just the
// shape. This is deliberately the ONLY accepted way to describe how an
// application is built and run (FR-023 business rule): there is no field
// here, or ever intended to be, for raw Kubernetes/Docker/Nginx config.
type DeploymentYAML struct {
	App struct {
		Name  string `yaml:"name"`
		Owner string `yaml:"owner"`
	} `yaml:"app"`
	Services map[string]struct {
		Runtime string `yaml:"runtime"`
		Port    int    `yaml:"port"`
	} `yaml:"services"`
	Database struct {
		Type string `yaml:"type"`
	} `yaml:"database"`
	Scaling struct {
		Min *int `yaml:"min"`
		Max *int `yaml:"max"`
	} `yaml:"scaling"`
	Resources struct {
		Tier string `yaml:"tier"`
	} `yaml:"resources"`
	Domain struct {
		Visibility string `yaml:"visibility"`
	} `yaml:"domain"`
}

type StackKind string

const (
	StackKindFrontend StackKind = "frontend"
	StackKindBackend  StackKind = "backend"
	StackKindDatabase StackKind = "database"
	StackKindCache    StackKind = "cache"
)

type SupportedStack struct {
	ID     string
	Kind   StackKind
	Name   string
	Status string // active | deprecated | blocked
}

// CheckStatus mirrors the per-sub-check outcome in a ValidationReport
// (FR-034): every sub-check reports one of these, "skipped" being used
// honestly rather than faking a pass for checks this state doesn't yet
// implement (e.g. FR-032 quota — see ValidationReport doc comment).
type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckFailed  CheckStatus = "failed"
	CheckSkipped CheckStatus = "skipped"
)

type ValidationCheck struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Details []string    `json:"details,omitempty"`
}

// ValidationReport implements FR-034 (Validation Result Reporting) over the
// sub-checks defined in Module H. FR-032 (Resource Quota Validation) is
// reported as "skipped" rather than a fabricated pass, because department
// quota numbers are TBD (DEC-014, docs/17_Decision_Log.md) and Module M
// (Resource Manager) doesn't exist yet.
type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Checks []ValidationCheck `json:"checks"`
}
