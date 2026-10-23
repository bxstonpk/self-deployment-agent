// Approvals implements signed approval decisions (DEC-017) — see
// internal/domain/approval_signing.go.
package service

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"platform-api/internal/domain"
)

type ApprovalRecords interface {
	Get(ctx context.Context, deploymentID string) (domain.DeploymentApproval, error)
	ListPending(ctx context.Context) ([]domain.DeploymentApproval, error)
	RecordSignature(ctx context.Context, deploymentID, fingerprint string, signedAt time.Time, signature string) error
}

type ApprovalLookups struct {
	Deployments interface {
		GetByID(ctx context.Context, id string) (domain.Deployment, error)
	}
	Builds interface {
		GetByID(ctx context.Context, id string) (domain.Build, error)
	}
	Apps interface {
		GetByID(ctx context.Context, id string) (domain.Application, error)
	}
	Users interface {
		GetByID(ctx context.Context, id string) (domain.User, error)
		GetByEmail(ctx context.Context, email string) (domain.User, error)
	}
}

type SignedDecider interface {
	DecideSignedApproval(ctx context.Context, deploymentID, approverID string, approve bool, reason, signerFingerprint string) (domain.Deployment, error)
}

type ApprovalService struct {
	records  ApprovalRecords
	lookups  ApprovalLookups
	decider  SignedDecider
	notifier AlertNotifier
	keys     []domain.ApproverKey
	now      func() time.Time
}

func NewApprovalService(records ApprovalRecords, lookups ApprovalLookups, decider SignedDecider, notifier AlertNotifier, keys []domain.ApproverKey) *ApprovalService {
	return &ApprovalService{records: records, lookups: lookups, decider: decider, notifier: notifier, keys: keys, now: time.Now}
}

// ApprovalRequest is everything an approver reviews, and the text they sign.
type ApprovalRequest struct {
	Subject         domain.ApprovalSubject
	Status          domain.DeploymentStatus
	Decision        domain.ApprovalDecision
	RequestedBy     string // email
	RequestedAt     time.Time
	ScanReports     map[string]domain.ScanReport
	SigningTemplate string
}

func (s *ApprovalService) keysOf(user domain.User) []domain.ApproverKey {
	out := []domain.ApproverKey{}
	for _, k := range s.keys {
		if strings.EqualFold(k.Email, user.Email) {
			out = append(out, k)
		}
	}
	return out
}

func (s *ApprovalService) requireApprover(user domain.User) error {
	if len(s.keysOf(user)) == 0 {
		return domain.ErrNotAnApprover
	}
	return nil
}

func (s *ApprovalService) request(ctx context.Context, a domain.DeploymentApproval) (ApprovalRequest, error) {
	d, err := s.lookups.Deployments.GetByID(ctx, a.DeploymentID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	b, err := s.lookups.Builds.GetByID(ctx, d.BuildID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	app, err := s.lookups.Apps.GetByID(ctx, d.ApplicationID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	requester := a.RequestedBy
	if u, err := s.lookups.Users.GetByID(ctx, a.RequestedBy); err == nil {
		requester = u.Email
	}
	subject := domain.ApprovalSubject{
		DeploymentID: d.ID, ApplicationID: app.ID, ApplicationName: app.Name, Environment: d.Environment,
		BuildID: b.ID, ImageRefs: b.ImageRefs, TestReport: a.TestReport,
	}
	return ApprovalRequest{Subject: subject, Status: d.Status, Decision: a.Decision, RequestedBy: requester,
		RequestedAt: a.CreatedAt, ScanReports: d.ScanReports, SigningTemplate: subject.SigningTemplate()}, nil
}

// Pending lists every deployment waiting for a decision.
func (s *ApprovalService) Pending(ctx context.Context, caller domain.User) ([]ApprovalRequest, error) {
	if err := s.requireApprover(caller); err != nil {
		return nil, err
	}
	pending, err := s.records.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	out := []ApprovalRequest{}
	for _, a := range pending {
		r, err := s.request(ctx, a)
		if err != nil {
			log.Printf("approvals: %s: %v", a.DeploymentID, err)
			continue
		}
		if r.Status == domain.DeploymentPendingApproval {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *ApprovalService) Request(ctx context.Context, caller domain.User, deploymentID string) (ApprovalRequest, error) {
	if err := s.requireApprover(caller); err != nil {
		return ApprovalRequest{}, err
	}
	a, err := s.records.Get(ctx, deploymentID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	return s.request(ctx, a)
}

// Decide verifies a signed decision and applies it. The text is rebuilt
// here from the platform's own records, so the signature only verifies if
// the approver signed exactly this deployment, these images and this test
// report — and only with a key registered to the signed-in caller.
func (s *ApprovalService) Decide(ctx context.Context, caller domain.User, deploymentID, decision, reason, signedAt, signature string) (domain.Deployment, string, error) {
	keys := s.keysOf(caller)
	if len(keys) == 0 {
		return domain.Deployment{}, "", domain.ErrNotAnApprover
	}
	if decision != "approve" && decision != "reject" {
		return domain.Deployment{}, "", errors.New(`decision must be "approve" or "reject"`)
	}
	at, err := time.Parse(time.RFC3339, signedAt)
	if err != nil {
		return domain.Deployment{}, "", domain.ErrApprovalSignature
	}
	if d := s.now().Sub(at); d > domain.MaxApprovalClockSkew || d < -domain.MaxApprovalClockSkew {
		return domain.Deployment{}, "", domain.ErrApprovalSignatureStale
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return domain.Deployment{}, "", domain.ErrApprovalSignature
	}
	req, err := s.Request(ctx, caller, deploymentID)
	if err != nil {
		return domain.Deployment{}, "", err
	}
	if req.Status != domain.DeploymentPendingApproval {
		return domain.Deployment{}, "", domain.ErrDeploymentNotPendingApproval
	}
	message := req.Subject.SignedMessage(decision, reason, signedAt)
	var signer *domain.ApproverKey
	for i := range keys {
		if ed25519.Verify(keys[i].PublicKey, message, sig) {
			signer = &keys[i]
			break
		}
	}
	if signer == nil {
		return domain.Deployment{}, "", domain.ErrApprovalSignature
	}
	deployment, err := s.decider.DecideSignedApproval(ctx, deploymentID, caller.ID, decision == "approve", reason, signer.Fingerprint)
	if err != nil {
		return domain.Deployment{}, "", err
	}
	if err := s.records.RecordSignature(ctx, deploymentID, signer.Fingerprint, at, signature); err != nil {
		log.Printf("approvals: record signature for %s: %v", deploymentID, err)
	}
	return deployment, signer.Fingerprint, nil
}

// NotifyApprovers tells every platform administrator with a key that a
// deployment is waiting for them, and how to review it.
func (s *ApprovalService) NotifyApprovers(ctx context.Context, app domain.Application, deployment domain.Deployment) {
	seen := map[string]bool{}
	for _, k := range s.keys {
		if seen[k.Email] {
			continue
		}
		seen[k.Email] = true
		u, err := s.lookups.Users.GetByEmail(ctx, k.Email)
		if err != nil {
			log.Printf("approvals: approver %s has no account yet", k.Email)
			continue
		}
		s.notifier.NotifyUser(ctx, u.ID, domain.NotificationApprovalRequest,
			fmt.Sprintf("%s: %s deployment awaiting your signed approval", app.Name, deployment.Environment),
			fmt.Sprintf("Review and decide with: company-deploy approve %s", deployment.ID),
			"deployment", deployment.ID)
	}
}
