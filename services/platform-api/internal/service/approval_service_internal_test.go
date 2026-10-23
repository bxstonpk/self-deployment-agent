package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
)

type memApprovalRecords struct {
	a      domain.DeploymentApproval
	signed string
}

func (m *memApprovalRecords) Get(ctx context.Context, id string) (domain.DeploymentApproval, error) {
	if id != m.a.DeploymentID {
		return domain.DeploymentApproval{}, domain.ErrNotFound
	}
	return m.a, nil
}
func (m *memApprovalRecords) ListPending(ctx context.Context) ([]domain.DeploymentApproval, error) {
	return []domain.DeploymentApproval{m.a}, nil
}
func (m *memApprovalRecords) RecordSignature(ctx context.Context, id, fp string, at time.Time, sig string) error {
	m.signed = fp
	return nil
}

type approvalLookupFakes struct {
	deployment domain.Deployment
	build      domain.Build
}

func (f *approvalLookupFakes) GetDeployment(ctx context.Context, id string) (domain.Deployment, error) {
	return f.deployment, nil
}

type depLookup struct{ f *approvalLookupFakes }

func (d depLookup) GetByID(ctx context.Context, id string) (domain.Deployment, error) {
	return d.f.deployment, nil
}

type buildLookup struct{ f *approvalLookupFakes }

func (b buildLookup) GetByID(ctx context.Context, id string) (domain.Build, error) {
	return b.f.build, nil
}

type appLookup struct{}

func (appLookup) GetByID(ctx context.Context, id string) (domain.Application, error) {
	return domain.Application{ID: "app-1", Name: "payroll"}, nil
}

type userLookup struct{}

func (userLookup) GetByID(ctx context.Context, id string) (domain.User, error) {
	return domain.User{ID: id, Email: id + "@sti-th.com"}, nil
}
func (userLookup) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	return domain.User{ID: "admin-user", Email: email}, nil
}

type memDecider struct {
	calls    int
	approved bool
	fp       string
}

func (m *memDecider) DecideSignedApproval(ctx context.Context, id, approver string, approve bool, reason, fp string) (domain.Deployment, error) {
	m.calls++
	m.approved, m.fp = approve, fp
	return domain.Deployment{ID: id, Status: domain.DeploymentRunning}, nil
}

type approvalFixture struct {
	svc     *ApprovalService
	records *memApprovalRecords
	lookups *approvalLookupFakes
	decider *memDecider
	priv    ed25519.PrivateKey
	admin   domain.User
	clock   time.Time
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys, err := domain.ParseApproverKeys("Admin@sti-th.com=ed25519:" + base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatal(err)
	}
	fx := &approvalFixture{
		records: &memApprovalRecords{a: domain.DeploymentApproval{DeploymentID: "dep-1", RequestedBy: "dev",
			Decision: domain.ApprovalPending, TestReport: "go test ./... ok\nnpm test: 42 passed"}},
		lookups: &approvalLookupFakes{
			deployment: domain.Deployment{ID: "dep-1", ApplicationID: "app-1", BuildID: "build-1",
				Environment: domain.EnvironmentProduction, Status: domain.DeploymentPendingApproval},
			build: domain.Build{ID: "build-1", ImageRefs: map[string]string{"api": "platform-build/payroll-api:b1", "frontend": "platform-build/payroll-frontend:b1"}},
		},
		decider: &memDecider{},
		priv:    priv,
		admin:   domain.User{ID: "admin-user", Email: "admin@sti-th.com"},
		clock:   time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC),
	}
	fx.svc = NewApprovalService(fx.records, ApprovalLookups{Deployments: depLookup{fx.lookups}, Builds: buildLookup{fx.lookups},
		Apps: appLookup{}, Users: userLookup{}}, fx.decider, &memAlertNotifier{}, keys)
	fx.svc.now = func() time.Time { return fx.clock }
	return fx
}

func (fx *approvalFixture) sign(t *testing.T, priv ed25519.PrivateKey, decision, reason string, at time.Time) (string, string) {
	t.Helper()
	req, err := fx.svc.Request(context.Background(), fx.admin, "dep-1")
	if err != nil {
		t.Fatal(err)
	}
	signedAt := at.UTC().Format(time.RFC3339)
	sig := ed25519.Sign(priv, req.Subject.SignedMessage(decision, reason, signedAt))
	return signedAt, base64.StdEncoding.EncodeToString(sig)
}

func TestSignedApproval_TheHolderOfTheKeyApproves(t *testing.T) {
	fx := newApprovalFixture(t)
	at, sig := fx.sign(t, fx.priv, "approve", "all tests passed", fx.clock)
	_, fp, err := fx.svc.Decide(context.Background(), fx.admin, "dep-1", "approve", "all tests passed", at, sig)
	if err != nil {
		t.Fatal(err)
	}
	if fx.decider.calls != 1 || !fx.decider.approved || fp == "" || fx.records.signed != fp {
		t.Fatalf("the decision should go through and its signer be recorded, got %+v", fx.decider)
	}
}

func TestSignedApproval_RefusesEverythingElse(t *testing.T) {
	ctx := context.Background()
	fx := newApprovalFixture(t)
	at, sig := fx.sign(t, fx.priv, "approve", "", fx.clock)

	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	otherAt, otherSig := fx.sign(t, otherPriv, "approve", "", fx.clock)
	cases := []struct {
		name string
		do   func() error
		want error
	}{
		{"someone who holds no key", func() error {
			_, _, err := fx.svc.Decide(ctx, domain.User{ID: "dev", Email: "dev@sti-th.com"}, "dep-1", "approve", "", at, sig)
			return err
		}, domain.ErrNotAnApprover},
		{"a different private key", func() error {
			_, _, err := fx.svc.Decide(ctx, fx.admin, "dep-1", "approve", "", otherAt, otherSig)
			return err
		}, domain.ErrApprovalSignature},
		{"an approve signature presented as a reject", func() error {
			_, _, err := fx.svc.Decide(ctx, fx.admin, "dep-1", "reject", "", at, sig)
			return err
		}, domain.ErrApprovalSignature},
		{"a different reason than was signed", func() error {
			_, _, err := fx.svc.Decide(ctx, fx.admin, "dep-1", "approve", "added later", at, sig)
			return err
		}, domain.ErrApprovalSignature},
		{"a replay twenty minutes later", func() error {
			fx.clock = fx.clock.Add(20 * time.Minute)
			defer func() { fx.clock = fx.clock.Add(-20 * time.Minute) }()
			_, _, err := fx.svc.Decide(ctx, fx.admin, "dep-1", "approve", "", at, sig)
			return err
		}, domain.ErrApprovalSignatureStale},
		{"the deployment rebuilt with other images", func() error {
			old := fx.lookups.build.ImageRefs["api"]
			fx.lookups.build.ImageRefs["api"] = "platform-build/payroll-api:b2"
			defer func() { fx.lookups.build.ImageRefs["api"] = old }()
			_, _, err := fx.svc.Decide(ctx, fx.admin, "dep-1", "approve", "", at, sig)
			return err
		}, domain.ErrApprovalSignature},
		{"a test report swapped after signing", func() error {
			old := fx.records.a.TestReport
			fx.records.a.TestReport = "everything is fine, trust me"
			defer func() { fx.records.a.TestReport = old }()
			_, _, err := fx.svc.Decide(ctx, fx.admin, "dep-1", "approve", "", at, sig)
			return err
		}, domain.ErrApprovalSignature},
	}
	for _, c := range cases {
		if err := c.do(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if fx.decider.calls != 0 {
		t.Fatalf("no refused decision may reach the deployment, %d did", fx.decider.calls)
	}
}

func TestParseApproverKeys(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	good := "a@x.com=ed25519:" + base64.StdEncoding.EncodeToString(pub)
	keys, err := domain.ParseApproverKeys(good + ", " + good)
	if err != nil || len(keys) != 2 || keys[0].Fingerprint == "" {
		t.Fatalf("got %v, %v", keys, err)
	}
	for _, bad := range []string{"a@x.com", "a@x.com=rsa:abc", "a@x.com=ed25519:notbase64!", "nobody=ed25519:" + base64.StdEncoding.EncodeToString(pub)} {
		if _, err := domain.ParseApproverKeys(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if keys, err := domain.ParseApproverKeys(""); err != nil || len(keys) != 0 {
		t.Fatalf("empty means no approvers, got %v %v", keys, err)
	}
}
