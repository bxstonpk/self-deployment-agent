package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// Development governance: the company standard from the moment an
// application is started (migration 0024).

// ApplicationProfile is what an application is for, recorded when it starts.
type ApplicationProfile struct {
	ApplicationID       string
	Purpose             string
	UsersDescription    string
	HandlesPersonalData bool
	DataDescription     string
	Frontend            string
	Backend             string
	Database            string
	UpdatedBy           string
	UpdatedAt           time.Time
}

// DependencyRule is a library the company doesn't allow.
type DependencyRule struct {
	ID        string
	Ecosystem string // npm | go | pypi
	Package   string
	Reason    string
	CreatedBy string
	CreatedAt time.Time

	CreatedByEmail string // filled by listings
}

// StandardException is an application's request to step outside the
// standard, and the administrator's decision on it.
type StandardException struct {
	ID             string
	ApplicationID  string
	RequestedBy    string
	Kind           string // dependency | stack | other
	Subject        string // for a dependency, "<ecosystem>:<package>"
	Reason         string
	Status         string // pending | approved | rejected
	DecidedBy      *string
	DecisionReason string
	CreatedAt      time.Time
	DecidedAt      *time.Time

	// Filled by listings, for whoever reads them.
	ApplicationName  string
	RequestedByEmail string
	DecidedByEmail   string
}

// Dependency is one library a project's source declares.
type Dependency struct {
	Ecosystem string
	Package   string
	Service   string // the service folder it was found in
	Manifest  string // e.g. api/package.json
}

const (
	ExceptionPending  = "pending"
	ExceptionApproved = "approved"
	ExceptionRejected = "rejected"

	NotificationStandardException NotificationCategory = "standard_exception"

	AuditActionRecordProfile        AuditAction = "governance.profile_recorded"
	AuditActionDependencyRuleAdd    AuditAction = "governance.dependency_rule_add"
	AuditActionDependencyRuleRemove AuditAction = "governance.dependency_rule_remove"
	AuditActionRequestException     AuditAction = "governance.exception_request"
	AuditActionDecideException      AuditAction = "governance.exception_decision"
)

var (
	ErrInvalidProfile          = errors.New("a profile needs a purpose (what the application is for), and says whether it handles personal data")
	ErrInvalidDependencyRule   = errors.New("a dependency rule needs an ecosystem (npm, go or pypi), a package name and a reason")
	ErrInvalidException        = errors.New("an exception needs a kind (dependency, stack or other), what it is for, and why; a dependency is <ecosystem>:<package>")
	ErrExceptionNotPending     = errors.New("this exception has already been decided")
	ErrDependencyNotAllowed    = errors.New("the source uses libraries the company doesn't allow")
	ErrDependencyRuleExists    = errors.New("there is already a rule for this library")
	ErrProfileRequired         = errors.New("this application has no profile yet: start it with start_project (what it is for, who uses it, whether it handles personal data) before building it")
	ErrUnreadableSource        = errors.New("the source can't be checked")
	ErrRejectionReasonRequired = errors.New("say why you're rejecting it, so the owner knows what to change")
)

var pypiSeparators = regexp.MustCompile(`[-_.]+`)

// NormalizePackage is how a package name is compared within its ecosystem:
// npm and PyPI names are case-insensitive (PyPI also treats -, _ and . as the
// same, PEP 503); Go module paths are compared as written.
func NormalizePackage(ecosystem, name string) string {
	name = strings.TrimSpace(name)
	switch ecosystem {
	case "npm":
		return strings.ToLower(name)
	case "pypi":
		return pypiSeparators.ReplaceAllString(strings.ToLower(name), "-")
	default:
		return name
	}
}

// ValidEcosystem reports whether ecosystem is one dependency rules cover.
func ValidEcosystem(ecosystem string) bool {
	return ecosystem == "npm" || ecosystem == "go" || ecosystem == "pypi"
}

// DependencySubject is how a dependency exception names its library.
func DependencySubject(ecosystem, pkg string) string {
	return ecosystem + ":" + NormalizePackage(ecosystem, pkg)
}

// CompanyStandardRules is the standard in words, for whoever starts an
// application (GET /standards). The catalog and the dependency rules are
// the enforced parts; these say what they enforce, and the rest.
// HashRoutingSubject is the exception subject (kind "other") that lets an
// application use hash routing (service/routecheck.go).
const HashRoutingSubject = "hash-routing"

// ErrHashRouting: the frontend routes on the address's # (/#/page).
var ErrHashRouting = errors.New("hash routing (/#/ addresses) isn't allowed")

var CompanyStandardRules = []string{
	"Start every application with start_project, and its code from the company templates (scaffold_project).",
	"A React frontend, one API in Python, JavaScript (Node.js), TypeScript or Go, and a MySQL database: the supported stacks list is enforced at validation.",
	"The API listens on $PORT and reads its database from DATABASE_HOST/PORT/NAME/USER/PASSWORD; the platform provides both.",
	"No password, key or token in the source or in deployment.yaml; other secrets go in the Portal's Secrets.",
	"Libraries on the company's dependency rules can't be used without an approved exception: the build fails.",
	"Anything outside the standard needs an exception (request_exception), decided by the administrator, before it is used.",
	"Every deployment that waits for approval carries a REVIEW.md: workflow, structure and the path of every file, data, what changed.",
	"Tests run before every deployment, and their results go to the approver.",
	"The API is RESTful: resources are nouns (/items, /items/{id}), the method says what happens (GET reads, POST creates, PUT/PATCH change, DELETE removes), bodies are JSON, status codes mean what they say (201 created, 400 bad input, 404 not found), and searching, filtering, sorting and paging are query parameters on the collection (GET /items?q=pen&status=open&page=2).",
	"Frontend pages are paths (/{app}/items/7), never hash routes (/#/items/7): the build refuses HashRouter, createHashRouter, createWebHashHistory and createHashHistory. What a page shows (search, filters, sort, page, the open tab) is in the address's query (?q=pen&status=open), read from it when the page opens, so Back, a refresh or a shared link shows the same thing.",
}
