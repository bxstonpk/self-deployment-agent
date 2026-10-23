// Auth implements internal/domain/auth.go: password sign-in, sessions,
// API keys, and the device authorization each Claude Code machine uses to
// fetch its key. See DEC-001/DEC-003 (revised 2026-10-23) in
// docs/17_Decision_Log.md for why the platform authenticates at all now.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"platform-api/internal/domain"
)

type AuthRepository interface {
	UserByEmail(ctx context.Context, email string) (domain.User, string, error)
	CreateUser(ctx context.Context, email, fullName, departmentID, passwordHash string) (domain.User, error)
	ClaimUser(ctx context.Context, userID, passwordHash string) (bool, error)
	RecordLogin(ctx context.Context, userID string) error

	CreateCredential(ctx context.Context, c domain.Credential, tokenHash string) (domain.Credential, error)
	CredentialByHash(ctx context.Context, tokenHash string) (domain.Credential, domain.User, error)
	TouchCredential(ctx context.Context, id string) error
	ListCredentials(ctx context.Context, userID string, kind domain.CredentialKind) ([]domain.Credential, error)
	RevokeCredential(ctx context.Context, userID, id string) (domain.Credential, error)

	CreateDevice(ctx context.Context, d domain.DeviceAuthorization, deviceCodeHash string) (domain.DeviceAuthorization, error)
	DeviceByCodeHash(ctx context.Context, deviceCodeHash string) (domain.DeviceAuthorization, error)
	PendingDeviceByUserCode(ctx context.Context, userCode string) (domain.DeviceAuthorization, error)
	DecideDevice(ctx context.Context, id, userID string, approve bool) (domain.DeviceAuthorization, error)
	ClaimApprovedDevice(ctx context.Context, id string) (string, error)
	SetDeviceCredential(ctx context.Context, id, credentialID string) error
}

type AuthDepartments interface {
	GetOrCreateByName(ctx context.Context, name string) (domain.Department, error)
}

type AuthUsers interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

// AuthPolicy is the deployment-specific part of authentication, all of it
// set by IT in the platform's environment.
type AuthPolicy struct {
	// EnrollmentCode is the shared code IT hands to employees; registering
	// without it is refused, so being on the LAN isn't enough to get an
	// account.
	EnrollmentCode string
	// AllowedEmailDomains, if non-empty, limits registration to these
	// domains (e.g. "sti-th.com").
	AllowedEmailDomains []string
	SessionTTL          time.Duration
	APIKeyTTL           time.Duration // default lifetime of a key
	APIKeyMaxTTL        time.Duration // the most an employee may ask for
	DeviceCodeTTL       time.Duration
	DevicePollInterval  time.Duration
	// MaxFailedLogins within FailedLoginWindow locks an email out for
	// the rest of that window.
	MaxFailedLogins   int
	FailedLoginWindow time.Duration
}

func DefaultAuthPolicy() AuthPolicy {
	return AuthPolicy{
		SessionTTL:         12 * time.Hour,
		APIKeyTTL:          30 * 24 * time.Hour,
		APIKeyMaxTTL:       90 * 24 * time.Hour,
		DeviceCodeTTL:      10 * time.Minute,
		DevicePollInterval: 5 * time.Second,
		MaxFailedLogins:    5,
		FailedLoginWindow:  15 * time.Minute,
	}
}

type AuthService struct {
	repo        AuthRepository
	users       AuthUsers
	departments AuthDepartments
	audit       AuditRecorder
	policy      AuthPolicy
	now         func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time // lower-cased email -> recent failed sign-ins
}

func NewAuthService(repo AuthRepository, users AuthUsers, departments AuthDepartments, audit AuditRecorder, policy AuthPolicy) *AuthService {
	return &AuthService{
		repo: repo, users: users, departments: departments, audit: audit, policy: policy,
		now: time.Now, failures: map[string][]time.Time{},
	}
}

const (
	sessionTokenPrefix = "cps_"
	apiKeyTokenPrefix  = "cpk_"
	// shown in lists: the type prefix plus 8 characters of the secret part
	visiblePrefixLen = 12
)

// IssuedCredential is a credential together with its token — returned
// exactly once, at creation, and never again.
type IssuedCredential struct {
	Token      string
	Credential domain.Credential
	User       domain.User
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// userCodeAlphabet leaves out vowels (no accidental words) and letters
// easily misread for each other, per RFC 8628 §6.1's advice.
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

func randomUserCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate user code: %w", err)
	}
	var b strings.Builder
	for i, v := range raw {
		if i == 4 {
			b.WriteByte('-')
		}
		b.WriteByte(userCodeAlphabet[int(v)%len(userCodeAlphabet)])
	}
	return b.String(), nil
}

// NormalizeUserCode accepts what a person types: any case, with or
// without the dash, with stray spaces.
func NormalizeUserCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) == 8 {
		return s[:4] + "-" + s[4:]
	}
	return s
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *AuthService) emailAllowed(email string) bool {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	if len(s.policy.AllowedEmailDomains) == 0 {
		return true
	}
	domainPart := email[at+1:]
	for _, d := range s.policy.AllowedEmailDomains {
		if strings.EqualFold(domainPart, strings.TrimSpace(d)) {
			return true
		}
	}
	return false
}

func (s *AuthService) record(ctx context.Context, actorID string, action domain.AuditAction, resourceID string, outcome domain.AuditOutcome, detail string) {
	if s.audit == nil || actorID == "" {
		return
	}
	if err := s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: actorID, Action: action, ResourceType: "credential",
		ResourceID: resourceID, Outcome: outcome, Detail: detail,
	}); err != nil {
		log.Printf("auth: audit %s: %v", action, err)
	}
}

func (s *AuthService) issue(ctx context.Context, user domain.User, kind domain.CredentialKind, name string, ttl time.Duration) (IssuedCredential, error) {
	prefix := sessionTokenPrefix
	if kind == domain.CredentialAPIKey {
		prefix = apiKeyTokenPrefix
	}
	token, err := randomToken(prefix)
	if err != nil {
		return IssuedCredential{}, err
	}
	cred, err := s.repo.CreateCredential(ctx, domain.Credential{
		UserID: user.ID, Kind: kind, Name: name, Prefix: token[:visiblePrefixLen],
		ExpiresAt: s.now().Add(ttl),
	}, hashToken(token))
	if err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{Token: token, Credential: cred, User: user}, nil
}

type RegisterRequest struct {
	Email          string
	Password       string
	FullName       string
	Department     string
	EnrollmentCode string
}

// Register creates an account, or claims one the old self-declared
// identity created (it has applications but no password), and signs it
// in. Either way the enrollment code is required.
func (s *AuthService) Register(ctx context.Context, req RegisterRequest) (IssuedCredential, error) {
	if s.policy.EnrollmentCode == "" ||
		subtle.ConstantTimeCompare([]byte(req.EnrollmentCode), []byte(s.policy.EnrollmentCode)) != 1 {
		return IssuedCredential{}, domain.ErrEnrollmentCode
	}
	email := normalizeEmail(req.Email)
	if !s.emailAllowed(email) {
		return IssuedCredential{}, domain.ErrEmailNotAllowed
	}
	if n := len(req.Password); n < 10 || n > 72 { // bcrypt reads at most 72 bytes
		return IssuedCredential{}, domain.ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return IssuedCredential{}, fmt.Errorf("hash password: %w", err)
	}

	existing, existingHash, err := s.repo.UserByEmail(ctx, email)
	var user domain.User
	switch {
	case err == nil && existingHash != "":
		return IssuedCredential{}, domain.ErrEmailRegistered
	case err == nil:
		claimed, err := s.repo.ClaimUser(ctx, existing.ID, string(hash))
		if err != nil {
			return IssuedCredential{}, err
		}
		if !claimed {
			return IssuedCredential{}, domain.ErrEmailRegistered
		}
		user = existing
	case errors.Is(err, domain.ErrNotFound):
		fullName := strings.TrimSpace(req.FullName)
		if fullName == "" {
			fullName = strings.SplitN(email, "@", 2)[0]
		}
		deptName := strings.TrimSpace(req.Department)
		if deptName == "" {
			deptName = "Unassigned"
		}
		dept, err := s.departments.GetOrCreateByName(ctx, deptName)
		if err != nil {
			return IssuedCredential{}, err
		}
		user, err = s.repo.CreateUser(ctx, email, fullName, dept.ID, string(hash))
		if err != nil {
			return IssuedCredential{}, err
		}
	default:
		return IssuedCredential{}, err
	}

	issued, err := s.issue(ctx, user, domain.CredentialSession, "portal", s.policy.SessionTTL)
	if err != nil {
		return IssuedCredential{}, err
	}
	s.record(ctx, user.ID, domain.AuditActionRegister, user.ID, domain.AuditSuccess, "password set")
	return issued, nil
}

// lockedOut reports, and prunes, the recent failures for an email.
func (s *AuthService) lockedOut(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-s.policy.FailedLoginWindow)
	recent := s.failures[email][:0]
	for _, t := range s.failures[email] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(s.failures, email)
		return false
	}
	s.failures[email] = recent
	return len(recent) >= s.policy.MaxFailedLogins
}

func (s *AuthService) noteFailure(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[email] = append(s.failures[email], s.now())
}

// Login checks a password and issues a session. Failures are counted per
// email, held in memory: a restart forgets them, which on a single host is
// an acceptable trade for not writing on every wrong guess.
func (s *AuthService) Login(ctx context.Context, email, password string) (IssuedCredential, error) {
	email = normalizeEmail(email)
	if s.lockedOut(email) {
		return IssuedCredential{}, domain.ErrAccountLocked
	}
	user, hash, err := s.repo.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return IssuedCredential{}, err
	}
	if err != nil || hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.noteFailure(email)
		if user.ID != "" {
			s.record(ctx, user.ID, domain.AuditActionLogin, user.ID, domain.AuditFailure, "wrong password")
		}
		return IssuedCredential{}, domain.ErrInvalidCredentials
	}
	if user.Status != "active" {
		return IssuedCredential{}, domain.ErrInvalidCredentials
	}
	s.mu.Lock()
	delete(s.failures, email)
	s.mu.Unlock()

	issued, err := s.issue(ctx, user, domain.CredentialSession, "portal", s.policy.SessionTTL)
	if err != nil {
		return IssuedCredential{}, err
	}
	if err := s.repo.RecordLogin(ctx, user.ID); err != nil {
		log.Printf("auth: record login for %s: %v", user.ID, err)
	}
	s.record(ctx, user.ID, domain.AuditActionLogin, issued.Credential.ID, domain.AuditSuccess, "")
	return issued, nil
}

// Authenticate resolves a bearer token to its user and credential.
func (s *AuthService) Authenticate(ctx context.Context, token string) (domain.User, domain.Credential, error) {
	if !strings.HasPrefix(token, sessionTokenPrefix) && !strings.HasPrefix(token, apiKeyTokenPrefix) {
		return domain.User{}, domain.Credential{}, domain.ErrInvalidToken
	}
	cred, user, err := s.repo.CredentialByHash(ctx, hashToken(token))
	if err != nil {
		return domain.User{}, domain.Credential{}, err
	}
	if !cred.Active(s.now()) || user.Status != "active" {
		return domain.User{}, domain.Credential{}, domain.ErrInvalidToken
	}
	if err := s.repo.TouchCredential(ctx, cred.ID); err != nil {
		log.Printf("auth: touch credential %s: %v", cred.ID, err)
	}
	return user, cred, nil
}

// Logout revokes the credential the request was made with.
func (s *AuthService) Logout(ctx context.Context, user domain.User, cred domain.Credential) error {
	if _, err := s.repo.RevokeCredential(ctx, user.ID, cred.ID); err != nil {
		return err
	}
	s.record(ctx, user.ID, domain.AuditActionRevokeCredential, cred.ID, domain.AuditSuccess, "signed out ("+string(cred.Kind)+")")
	return nil
}

func requireSession(cred domain.Credential) error {
	if cred.Kind != domain.CredentialSession {
		return domain.ErrSessionRequired
	}
	return nil
}

func (s *AuthService) keyTTL(requestedDays int) time.Duration {
	if requestedDays <= 0 {
		return s.policy.APIKeyTTL
	}
	ttl := time.Duration(requestedDays) * 24 * time.Hour
	if ttl > s.policy.APIKeyMaxTTL {
		return s.policy.APIKeyMaxTTL
	}
	return ttl
}

func cleanName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// CreateAPIKey issues a key from the Portal — for a machine that can't
// run the device flow. Only a signed-in session may do it.
func (s *AuthService) CreateAPIKey(ctx context.Context, user domain.User, cred domain.Credential, name string, days int) (IssuedCredential, error) {
	if err := requireSession(cred); err != nil {
		return IssuedCredential{}, err
	}
	issued, err := s.issue(ctx, user, domain.CredentialAPIKey, cleanName(name, "api key"), s.keyTTL(days))
	if err != nil {
		return IssuedCredential{}, err
	}
	s.record(ctx, user.ID, domain.AuditActionIssueAPIKey, issued.Credential.ID, domain.AuditSuccess,
		fmt.Sprintf("%q from the portal, expires %s", issued.Credential.Name, issued.Credential.ExpiresAt.Format(time.RFC3339)))
	return issued, nil
}

func (s *AuthService) ListAPIKeys(ctx context.Context, user domain.User, cred domain.Credential) ([]domain.Credential, error) {
	if err := requireSession(cred); err != nil {
		return nil, err
	}
	return s.repo.ListCredentials(ctx, user.ID, domain.CredentialAPIKey)
}

func (s *AuthService) RevokeAPIKey(ctx context.Context, user domain.User, cred domain.Credential, keyID string) (domain.Credential, error) {
	if err := requireSession(cred); err != nil {
		return domain.Credential{}, err
	}
	revoked, err := s.repo.RevokeCredential(ctx, user.ID, keyID)
	if err != nil {
		return domain.Credential{}, err
	}
	if revoked.Kind != domain.CredentialAPIKey {
		return domain.Credential{}, domain.ErrCredentialNotFound
	}
	s.record(ctx, user.ID, domain.AuditActionRevokeCredential, revoked.ID, domain.AuditSuccess, fmt.Sprintf("api key %q", revoked.Name))
	return revoked, nil
}

// DeviceStart is what a machine is told when it asks for a key.
type DeviceStart struct {
	DeviceCode string
	UserCode   string
	ExpiresIn  time.Duration
	Interval   time.Duration
}

// StartDevice begins a device authorization. Anyone may start one — it
// grants nothing until a signed-in employee approves its user code.
func (s *AuthService) StartDevice(ctx context.Context, clientName string) (DeviceStart, error) {
	deviceCode, err := randomToken("cpd_")
	if err != nil {
		return DeviceStart{}, err
	}
	// A collision with a live code is vanishingly rare (20^8 codes) but
	// cheap to retry, and the partial unique index catches it if it happens.
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		userCode, err := randomUserCode()
		if err != nil {
			return DeviceStart{}, err
		}
		_, lastErr = s.repo.CreateDevice(ctx, domain.DeviceAuthorization{
			UserCode: userCode, ClientName: cleanName(clientName, "unnamed machine"),
			ExpiresAt: s.now().Add(s.policy.DeviceCodeTTL),
		}, hashToken(deviceCode))
		if lastErr == nil {
			return DeviceStart{DeviceCode: deviceCode, UserCode: userCode,
				ExpiresIn: s.policy.DeviceCodeTTL, Interval: s.policy.DevicePollInterval}, nil
		}
	}
	return DeviceStart{}, lastErr
}

// PendingDevice shows a signed-in employee what they are about to approve.
func (s *AuthService) PendingDevice(ctx context.Context, cred domain.Credential, userCode string) (domain.DeviceAuthorization, error) {
	if err := requireSession(cred); err != nil {
		return domain.DeviceAuthorization{}, err
	}
	return s.repo.PendingDeviceByUserCode(ctx, NormalizeUserCode(userCode))
}

// DecideDevice approves or denies a pending device. Approving does not
// create the key yet: it's generated when the machine collects it, so it
// exists in plaintext only on its way to the machine.
func (s *AuthService) DecideDevice(ctx context.Context, user domain.User, cred domain.Credential, userCode string, approve bool) (domain.DeviceAuthorization, error) {
	if err := requireSession(cred); err != nil {
		return domain.DeviceAuthorization{}, err
	}
	pending, err := s.repo.PendingDeviceByUserCode(ctx, NormalizeUserCode(userCode))
	if err != nil {
		return domain.DeviceAuthorization{}, err
	}
	decided, err := s.repo.DecideDevice(ctx, pending.ID, user.ID, approve)
	if err != nil {
		return domain.DeviceAuthorization{}, err
	}
	outcome, verb := domain.AuditSuccess, "approved"
	if !approve {
		verb = "denied"
	}
	s.record(ctx, user.ID, domain.AuditActionDecideDevice, decided.ID, outcome, fmt.Sprintf("%s %q (%s)", verb, decided.ClientName, decided.UserCode))
	return decided, nil
}

// PollDevice is the machine collecting its key. Until the employee
// decides it answers ErrAuthorizationPending; once approved it issues the
// key exactly once.
func (s *AuthService) PollDevice(ctx context.Context, deviceCode string) (IssuedCredential, error) {
	device, err := s.repo.DeviceByCodeHash(ctx, hashToken(deviceCode))
	if err != nil {
		return IssuedCredential{}, err
	}
	switch device.Status {
	case domain.DeviceDenied:
		return IssuedCredential{}, domain.ErrDeviceDenied
	case domain.DeviceConsumed:
		return IssuedCredential{}, domain.ErrDeviceExpired
	case domain.DevicePending:
		if !s.now().Before(device.ExpiresAt) {
			return IssuedCredential{}, domain.ErrDeviceExpired
		}
		return IssuedCredential{}, domain.ErrAuthorizationPending
	}

	userID, err := s.repo.ClaimApprovedDevice(ctx, device.ID)
	if err != nil {
		return IssuedCredential{}, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return IssuedCredential{}, err
	}
	issued, err := s.issue(ctx, user, domain.CredentialAPIKey, device.ClientName, s.policy.APIKeyTTL)
	if err != nil {
		return IssuedCredential{}, err
	}
	if err := s.repo.SetDeviceCredential(ctx, device.ID, issued.Credential.ID); err != nil {
		log.Printf("auth: link device %s to credential %s: %v", device.ID, issued.Credential.ID, err)
	}
	s.record(ctx, user.ID, domain.AuditActionIssueAPIKey, issued.Credential.ID, domain.AuditSuccess,
		fmt.Sprintf("%q via device approval, expires %s", issued.Credential.Name, issued.Credential.ExpiresAt.Format(time.RFC3339)))
	return issued, nil
}
