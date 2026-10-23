// Signed approvals (DEC-017, decided 2026-10-23, docs/17_Decision_Log.md):
// a deployment that needs approval — production, by default — goes ahead
// only on a decision signed with a platform administrator's private key.
// The platform holds only the public key, so neither a stolen password, a
// stolen machine API key, nor anyone with the platform database can
// approve a deployment. Only whoever holds the private key can.
//
// What is signed binds the decision to exactly what was reviewed: the
// deployment, its build, every image that will run, and the test report
// the requester attached. A signature for one deployment, or for the same
// deployment rebuilt, does not verify for another.
package domain

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ApproverKey is one administrator's public key.
type ApproverKey struct {
	Email       string
	PublicKey   ed25519.PublicKey
	Fingerprint string
}

func KeyFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

// ParseApproverKeys reads PLATFORM_APPROVER_KEYS: comma-separated
// `email=ed25519:<base64 public key>` entries, as `company-deploy approver
// keygen` prints them.
func ParseApproverKeys(spec string) ([]ApproverKey, error) {
	keys := []ApproverKey{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		email, key, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "ed25519:") || !strings.Contains(email, "@") {
			return nil, fmt.Errorf("approver key %q: want email=ed25519:<base64>", entry)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(key, "ed25519:"))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("approver key for %s is not a base64 Ed25519 public key", email)
		}
		pub := ed25519.PublicKey(raw)
		keys = append(keys, ApproverKey{Email: strings.ToLower(strings.TrimSpace(email)), PublicKey: pub, Fingerprint: KeyFingerprint(pub)})
	}
	return keys, nil
}

// ApprovalSubject is everything an approval is about.
type ApprovalSubject struct {
	DeploymentID    string
	ApplicationID   string
	ApplicationName string
	Environment     Environment
	BuildID         string
	ImageRefs       map[string]string
	TestReport      string
}

func TestReportSHA256(report string) string {
	if report == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(report))
	return fmt.Sprintf("%x", sum[:])
}

// SigningTemplate is the exact text an approver signs, with three
// placeholders the signer fills in: {decision}, {reason}, {signed_at}.
// The platform hands it out and rebuilds it to verify, so the two sides
// can't drift apart.
func (s ApprovalSubject) SigningTemplate() string {
	var b strings.Builder
	b.WriteString("company-platform deployment approval v1\n")
	fmt.Fprintf(&b, "deployment: %s\n", s.DeploymentID)
	fmt.Fprintf(&b, "application: %s (%s)\n", s.ApplicationID, s.ApplicationName)
	fmt.Fprintf(&b, "environment: %s\n", s.Environment)
	fmt.Fprintf(&b, "build: %s\n", s.BuildID)
	services := make([]string, 0, len(s.ImageRefs))
	for svc := range s.ImageRefs {
		services = append(services, svc)
	}
	sort.Strings(services)
	for _, svc := range services {
		fmt.Fprintf(&b, "image: %s=%s\n", svc, s.ImageRefs[svc])
	}
	fmt.Fprintf(&b, "test-report-sha256: %s\n", TestReportSHA256(s.TestReport))
	b.WriteString("decision: {decision}\nreason: {reason}\nsigned-at: {signed_at}\n")
	return b.String()
}

// OneLine is how a reason appears in the signed text: one line, trimmed.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// SignedMessage fills the template.
func (s ApprovalSubject) SignedMessage(decision, reason, signedAt string) []byte {
	r := strings.NewReplacer("{decision}", decision, "{reason}", OneLine(reason), "{signed_at}", signedAt)
	return []byte(r.Replace(s.SigningTemplate()))
}

// MaxApprovalClockSkew bounds how old (or how far in the future) a
// signature's signed-at may be: a signature captured and replayed later
// is refused.
const MaxApprovalClockSkew = 10 * time.Minute

var (
	ErrSignedApprovalRequired = errors.New("this deployment must be approved with a platform administrator's signed decision (company-deploy approve)")
	ErrNotAnApprover          = errors.New("you are not a platform administrator who can approve deployments")
	ErrApprovalSignature      = errors.New("the signature does not match this deployment, decision and an approver key held by you")
	ErrApprovalSignatureStale = errors.New("the signature's signed-at time is too far from now; sign again")
	ErrTestReportTooLarge     = errors.New("the test report is larger than 64 KiB")
)

const MaxTestReportBytes = 64 * 1024
