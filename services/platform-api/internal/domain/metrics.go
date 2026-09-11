// Metrics implements Module T (Monitoring) — docs/02_Functional_Requirements.md
// FR-090 (collection) and FR-091 (access). Both sources are things the
// platform already sees, so an application needs no instrumentation of its
// own (FR-090's business rule):
//
//   - Resource use (CPU, memory) is sampled from the container runtime
//     itself, on an interval, for every container the platform started.
//   - Traffic (request count, error rate, latency) is measured at the
//     platform's own proxy — Module L's /run/{app}/{service} address, which
//     every request to a deployed application already passes through.
//
// A reading that fails is a gap: nothing is written, and nothing is
// interpolated to cover it (FR-090's exception flow). A query reports when
// collection last succeeded, so an empty answer is never quietly mistaken
// for a quiet application (docs/13_API_Requirements.md §5.5's
// "partial/unavailable indicator").
//
// Deliberately not implemented here, rather than approximated:
//
//   - FR-092 (threshold alerting). The thresholds are specified nowhere —
//     FR-092 itself defers them to the non-functional requirements, which
//     don't set any for metrics — and alerts on invented numbers are worse
//     than none.
//   - FR-093 (platform-wide dashboard). It is explicitly an administrator
//     and auditor view, and those roles don't exist yet (DEC-001).
//   - NFR-032 retention. Its durations are TBD, so nothing is purged.
package domain

import (
	"errors"
	"time"
)

// ErrInvalidMetricsWindow is a from/to the caller got backwards.
var ErrInvalidMetricsWindow = errors.New("invalid metrics window")

// ContainerUsage is one reading the runtime took of one container, before
// it becomes a stored sample. Source carries the application, deployment
// and service tags Module S already puts on every application container.
type ContainerUsage struct {
	ContainerID string
	Source      LogSource
	CPUPercent  float64
	MemoryBytes int64
	MemoryLimit int64
}

// ResourceSample is one container's resource use at one moment, as the
// container runtime reported it.
type ResourceSample struct {
	ApplicationID string
	DeploymentID  string
	Service       string
	ContainerID   string
	SampledAt     time.Time
	CPUPercent    float64
	MemoryBytes   int64
	MemoryLimit   int64
}

// RequestBucket is one minute of traffic through the proxy, for one
// service of one application in one environment. Latency is the
// application's own response time: the wait for a cold start, when there
// is one, is a scale event (FR-056), not the application being slow.
type RequestBucket struct {
	ApplicationID  string
	Service        string
	Environment    Environment
	Minute         time.Time
	Requests       int64
	Errors         int64
	LatencyMsTotal float64
	LatencyMsMax   float64
}

// Errors counts responses the application itself failed to serve — 5xx,
// and the proxy's own 502 when the application didn't answer at all. A 4xx
// is the caller's request being wrong, which is not the application
// failing, so it counts as a request and not as an error.
func IsRequestError(status int) bool {
	return status >= 500
}

type MetricsQuery struct {
	ApplicationID string
	Service       string
	Environment   string
	From          time.Time
	To            time.Time
}

// Window bounds and row caps. These are implementation limits that keep one
// call from pulling an unbounded result (docs/13_API_Requirements.md §5.5),
// not the retention policy of NFR-032, which is still TBD.
const (
	DefaultMetricsWindow = time.Hour
	MaxMetricsWindow     = 7 * 24 * time.Hour
	MaxResourceSamples   = 2000
	MaxRequestBuckets    = 2000
)

// ServiceInstances is what "instance count" means on this platform today:
// one container per service, or none while it is scaled to zero. FR-054's
// horizontal scaling above one instance doesn't exist yet.
type ServiceInstances struct {
	Service      string
	Instances    int
	ScaledToZero bool
}

// MetricsSnapshot is one answer to FR-091: the series in the window, plus
// the current picture the MCP contract asks for (instance count, last
// scale event) and an honest account of whether collection is keeping up.
type MetricsSnapshot struct {
	From           time.Time
	To             time.Time
	Resource       []ResourceSample
	Traffic        []RequestBucket
	Instances      []ServiceInstances
	LastScaleEvent *ScaleEvent
	// LastSampleAt is when a resource sample for this application last
	// landed; nil means none ever has.
	LastSampleAt *time.Time
	// Collecting is false when resource sampling is not keeping up — the
	// caller is looking at an incomplete answer, and the note says so.
	Collecting bool
	Note       string
}
