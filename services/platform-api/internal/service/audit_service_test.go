package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// --- fake AuditRepository, no database required ---

type fakeAuditRepo struct {
	entries       []domain.AuditEntry
	nextSeq       int64
	recordErr     error
	queryErr      error
	chainOK       bool
	chainBrokenAt int64
}

func newFakeAuditRepo() *fakeAuditRepo {
	return &fakeAuditRepo{chainOK: true}
}

func (f *fakeAuditRepo) Record(ctx context.Context, entry domain.AuditEntry) (domain.AuditEntry, error) {
	if f.recordErr != nil {
		return domain.AuditEntry{}, f.recordErr
	}
	f.nextSeq++
	entry.Seq = f.nextSeq
	entry.ID = fmt.Sprintf("audit-%d", f.nextSeq)
	entry.OccurredAt = time.Now()
	f.entries = append(f.entries, entry)
	return entry, nil
}

func (f *fakeAuditRepo) Query(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEntry, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	var out []domain.AuditEntry
	for i := len(f.entries) - 1; i >= 0; i-- { // newest first, matches the real repo's ORDER BY seq DESC
		e := f.entries[i]
		if q.ActorUserID != "" && e.ActorUserID != q.ActorUserID {
			continue
		}
		if q.ResourceType != "" && e.ResourceType != q.ResourceType {
			continue
		}
		if q.ResourceID != "" && e.ResourceID != q.ResourceID {
			continue
		}
		if q.Action != "" && e.Action != q.Action {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeAuditRepo) VerifyChain(ctx context.Context) (bool, int64, error) {
	return f.chainOK, f.chainBrokenAt, nil
}

func newAuditService() (*service.AuditService, *fakeAuditRepo, *fakeOwnerRepo, *fakeDeploymentRepo, *fakeBuildRepo) {
	repo := newFakeAuditRepo()
	owners := newFakeOwnerRepo()
	deployments := newFakeDeploymentRepo()
	builds := newFakeBuildRepo()
	return service.NewAuditService(repo, owners, deployments, builds), repo, owners, deployments, builds
}

func TestAuditRecord_Success_WritesEntry(t *testing.T) {
	svc, repo, _, _, _ := newAuditService()

	err := svc.Record(context.Background(), domain.AuditEntry{
		ActorUserID: "user-1", Action: domain.AuditActionSuspend,
		ResourceType: "application", ResourceID: "app-1", Outcome: domain.AuditSuccess,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(repo.entries) != 1 {
		t.Fatalf("expected 1 entry recorded, got %d", len(repo.entries))
	}
	if repo.entries[0].Action != domain.AuditActionSuspend || repo.entries[0].ResourceID != "app-1" {
		t.Fatalf("unexpected entry recorded: %+v", repo.entries[0])
	}
}

func TestAuditRecord_RepoFailure_ReturnsWrappedError(t *testing.T) {
	svc, repo, _, _, _ := newAuditService()
	repo.recordErr = errors.New("db unavailable")

	err := svc.Record(context.Background(), domain.AuditEntry{ActorUserID: "user-1", Action: domain.AuditActionSuspend})
	if err == nil {
		t.Fatal("expected an error when the underlying repo write fails")
	}
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Fatalf("expected the underlying error to be wrapped, got: %v", err)
	}
}

func TestAuditQuery_VisibleToTheActorThemselves(t *testing.T) {
	svc, repo, _, _, _ := newAuditService()
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "user-1", Action: domain.AuditActionRegisterApplication, ResourceType: "application", ResourceID: "app-1"},
	}

	got, err := svc.Query(context.Background(), "user-1", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the actor to see their own entry, got %d entries", len(got))
	}
}

func TestAuditQuery_VisibleToAnApplicationOwnerEvenIfNotTheActor(t *testing.T) {
	svc, repo, owners, _, _ := newAuditService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
	}
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "someone-else", Action: domain.AuditActionInitiateDeploy, ResourceType: "application", ResourceID: "app-1"},
	}

	got, err := svc.Query(context.Background(), "owner-1", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the application owner to see the entry, got %d entries", len(got))
	}
}

// Real bug, found via manual verification once FR-017 (Co-Owner
// Management) made a second owner on one application possible for the
// first time: Query used to check e.ResourceType == "application"
// directly, so a deployment-scoped entry (deploy_service.go's
// auditDeployOutcome records resource_type="deployment", not
// "application") was invisible to every owner except whoever performed
// the deploy — even the application's own primary owner.
func TestAuditQuery_DeploymentScopedEntry_VisibleToApplicationOwnerEvenIfNotTheActor(t *testing.T) {
	svc, repo, owners, deployments, _ := newAuditService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "primary-owner", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
	}
	deployments.byID["dep-1"] = domain.Deployment{ID: "dep-1", ApplicationID: "app-1"}
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "co-owner-who-deployed", Action: domain.AuditActionInitiateDeploy, ResourceType: "deployment", ResourceID: "dep-1"},
	}

	got, err := svc.Query(context.Background(), "primary-owner", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the primary owner to see a co-owner's deployment-scoped entry, got %d entries", len(got))
	}
}

// Same fix, build-scoped entries (build_service.go records
// resource_type="build").
func TestAuditQuery_BuildScopedEntry_VisibleToApplicationOwnerEvenIfNotTheActor(t *testing.T) {
	svc, repo, owners, _, builds := newAuditService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "primary-owner", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
	}
	builds.byID["build-1"] = domain.Build{ID: "build-1", ApplicationID: "app-1"}
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "co-owner-who-built", Action: domain.AuditActionTriggerBuild, ResourceType: "build", ResourceID: "build-1"},
	}

	got, err := svc.Query(context.Background(), "primary-owner", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the primary owner to see a co-owner's build-scoped entry, got %d entries", len(got))
	}
}

func TestAuditQuery_HiddenFromNeitherActorNorOwner(t *testing.T) {
	svc, repo, owners, _, _ := newAuditService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
	}
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "someone-else", Action: domain.AuditActionInitiateDeploy, ResourceType: "application", ResourceID: "app-1"},
	}

	got, err := svc.Query(context.Background(), "an-uninvolved-employee", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an uninvolved employee to see nothing, got %d entries", len(got))
	}
}

func TestAuditQuery_RevokedOwnerNoLongerSeesEntries(t *testing.T) {
	svc, repo, owners, _, _ := newAuditService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "former-owner", OwnershipRole: domain.OwnerRolePrimary, Status: "revoked"},
	}
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "someone-else", Action: domain.AuditActionInitiateDeploy, ResourceType: "application", ResourceID: "app-1"},
	}

	got, err := svc.Query(context.Background(), "former-owner", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected a revoked owner to see nothing, got %d entries", len(got))
	}
}

func TestAuditExport_ProducesCSVAndRecordsTheExportItself(t *testing.T) {
	svc, repo, _, _, _ := newAuditService()
	repo.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "user-1", Action: domain.AuditActionSuspend, ResourceType: "application", ResourceID: "app-1", Outcome: domain.AuditSuccess},
	}

	csvBytes, err := svc.Export(context.Background(), "user-1", domain.AuditQuery{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	csvText := string(csvBytes)
	if !strings.Contains(csvText, "app-1") || !strings.Contains(csvText, string(domain.AuditActionSuspend)) {
		t.Fatalf("expected the exported CSV to contain the queried entry, got:\n%s", csvText)
	}

	// FR-105 main flow step 4: the export action itself is recorded.
	found := false
	for _, e := range repo.entries {
		if e.Action == domain.AuditActionExport && e.ActorUserID == "user-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the export itself to be recorded as a new audit entry")
	}
}

func TestAuditVerifyIntegrity_DelegatesToRepo(t *testing.T) {
	svc, repo, _, _, _ := newAuditService()
	repo.chainOK = false
	repo.chainBrokenAt = 7

	ok, brokenAt, err := svc.VerifyIntegrity(context.Background())
	if err != nil {
		t.Fatalf("VerifyIntegrity: %v", err)
	}
	if ok {
		t.Fatal("expected the reported chain state to be broken")
	}
	if brokenAt != 7 {
		t.Fatalf("expected brokenAtSeq 7, got %d", brokenAt)
	}
}
