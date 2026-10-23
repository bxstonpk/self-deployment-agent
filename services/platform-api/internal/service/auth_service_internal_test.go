package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"platform-api/internal/domain"
)

// fakeAuthRepo mirrors AuthRepo's contract in memory, including the
// conditions its SQL enforces (claim only a password-less account, decide
// only a pending unexpired device, consume an approval once).
type fakeAuthRepo struct {
	mu      sync.Mutex
	now     func() time.Time
	users   map[string]*fakeAuthUser
	creds   map[string]*fakeCred
	devices map[string]*fakeDevice
	seq     int
}

type fakeAuthUser struct {
	user domain.User
	hash string
}

type fakeCred struct {
	cred domain.Credential
	hash string
}

type fakeDevice struct {
	d    domain.DeviceAuthorization
	hash string
}

func newFakeAuthRepo(now func() time.Time) *fakeAuthRepo {
	return &fakeAuthRepo{now: now, users: map[string]*fakeAuthUser{}, creds: map[string]*fakeCred{}, devices: map[string]*fakeDevice{}}
}

func (f *fakeAuthRepo) id(prefix string) string {
	f.seq++
	return prefix + "-" + string(rune('a'+f.seq))
}

func (f *fakeAuthRepo) UserByEmail(ctx context.Context, email string) (domain.User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.user.Email, email) {
			return u.user, u.hash, nil
		}
	}
	return domain.User{}, "", domain.ErrNotFound
}

func (f *fakeAuthRepo) CreateUser(ctx context.Context, email, fullName, departmentID, passwordHash string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := domain.User{ID: f.id("user"), Email: email, FullName: fullName, DepartmentID: departmentID, Status: "active"}
	f.users[u.ID] = &fakeAuthUser{user: u, hash: passwordHash}
	return u, nil
}

// legacyUser is an account the old self-declared header created.
func (f *fakeAuthRepo) legacyUser(email string) domain.User {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := domain.User{ID: f.id("legacy"), Email: email, FullName: "Legacy", Status: "active"}
	f.users[u.ID] = &fakeAuthUser{user: u}
	return u
}

func (f *fakeAuthRepo) ClaimUser(ctx context.Context, userID, passwordHash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[userID]
	if u == nil || u.hash != "" {
		return false, nil
	}
	u.hash = passwordHash
	return true, nil
}

func (f *fakeAuthRepo) RecordLogin(ctx context.Context, userID string) error { return nil }

func (f *fakeAuthRepo) GetByID(ctx context.Context, id string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.users[id]; u != nil {
		return u.user, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (f *fakeAuthRepo) CreateCredential(ctx context.Context, c domain.Credential, tokenHash string) (domain.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.ID = f.id("cred")
	c.CreatedAt = f.now()
	f.creds[c.ID] = &fakeCred{cred: c, hash: tokenHash}
	return c, nil
}

func (f *fakeAuthRepo) CredentialByHash(ctx context.Context, tokenHash string) (domain.Credential, domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.creds {
		if c.hash == tokenHash {
			return c.cred, f.users[c.cred.UserID].user, nil
		}
	}
	return domain.Credential{}, domain.User{}, domain.ErrInvalidToken
}

func (f *fakeAuthRepo) TouchCredential(ctx context.Context, id string) error { return nil }

func (f *fakeAuthRepo) ListCredentials(ctx context.Context, userID string, kind domain.CredentialKind) ([]domain.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Credential
	for _, c := range f.creds {
		if c.cred.UserID == userID && c.cred.Kind == kind {
			out = append(out, c.cred)
		}
	}
	return out, nil
}

func (f *fakeAuthRepo) RevokeCredential(ctx context.Context, userID, id string) (domain.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.creds[id]
	if c == nil || c.cred.UserID != userID {
		return domain.Credential{}, domain.ErrCredentialNotFound
	}
	if c.cred.RevokedAt == nil {
		t := f.now()
		c.cred.RevokedAt = &t
	}
	return c.cred, nil
}

func (f *fakeAuthRepo) CreateDevice(ctx context.Context, d domain.DeviceAuthorization, deviceCodeHash string) (domain.DeviceAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d.ID = f.id("device")
	d.Status = domain.DevicePending
	d.CreatedAt = f.now()
	f.devices[d.ID] = &fakeDevice{d: d, hash: deviceCodeHash}
	return d, nil
}

func (f *fakeAuthRepo) DeviceByCodeHash(ctx context.Context, h string) (domain.DeviceAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.devices {
		if d.hash == h {
			return d.d, nil
		}
	}
	return domain.DeviceAuthorization{}, domain.ErrDeviceExpired
}

func (f *fakeAuthRepo) PendingDeviceByUserCode(ctx context.Context, userCode string) (domain.DeviceAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.devices {
		if d.d.UserCode == userCode && d.d.Status == domain.DevicePending && f.now().Before(d.d.ExpiresAt) {
			return d.d, nil
		}
	}
	return domain.DeviceAuthorization{}, domain.ErrUserCodeNotFound
}

func (f *fakeAuthRepo) DecideDevice(ctx context.Context, id, userID string, approve bool) (domain.DeviceAuthorization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.devices[id]
	if d == nil || d.d.Status != domain.DevicePending || !f.now().Before(d.d.ExpiresAt) {
		return domain.DeviceAuthorization{}, domain.ErrUserCodeNotFound
	}
	d.d.Status = domain.DeviceDenied
	if approve {
		d.d.Status = domain.DeviceApproved
	}
	d.d.UserID = userID
	return d.d, nil
}

func (f *fakeAuthRepo) ClaimApprovedDevice(ctx context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.devices[id]
	if d == nil || d.d.Status != domain.DeviceApproved {
		return "", domain.ErrDeviceExpired
	}
	d.d.Status = domain.DeviceConsumed
	return d.d.UserID, nil
}

func (f *fakeAuthRepo) SetDeviceCredential(ctx context.Context, id, credentialID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[id].d.CredentialID = credentialID
	return nil
}

type fakeAuthDepartments struct{}

func (fakeAuthDepartments) GetOrCreateByName(ctx context.Context, name string) (domain.Department, error) {
	return domain.Department{ID: "dept-" + name, Name: name}, nil
}

type authFixture struct {
	svc   *AuthService
	repo  *fakeAuthRepo
	clock time.Time
}

func (fx *authFixture) advance(d time.Duration) { fx.clock = fx.clock.Add(d) }

func newAuthFixture(t *testing.T, mutate ...func(*AuthPolicy)) *authFixture {
	t.Helper()
	fx := &authFixture{clock: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	now := func() time.Time { return fx.clock }
	fx.repo = newFakeAuthRepo(now)
	policy := DefaultAuthPolicy()
	policy.EnrollmentCode = "join-the-platform"
	for _, m := range mutate {
		m(&policy)
	}
	fx.svc = NewAuthService(fx.repo, fx.repo, fakeAuthDepartments{}, nil, policy)
	fx.svc.now = now
	return fx
}

func (fx *authFixture) register(t *testing.T, email string) IssuedCredential {
	t.Helper()
	issued, err := fx.svc.Register(context.Background(), RegisterRequest{
		Email: email, Password: "correct horse battery", EnrollmentCode: "join-the-platform",
	})
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return issued
}

func TestRegister_RequiresEnrollmentCodeDomainAndPassword(t *testing.T) {
	fx := newAuthFixture(t, func(p *AuthPolicy) { p.AllowedEmailDomains = []string{"sti-th.com"} })
	ctx := context.Background()
	cases := []struct {
		req  RegisterRequest
		want error
	}{
		{RegisterRequest{Email: "a@sti-th.com", Password: "long enough pw", EnrollmentCode: "wrong"}, domain.ErrEnrollmentCode},
		{RegisterRequest{Email: "a@sti-th.com", Password: "long enough pw"}, domain.ErrEnrollmentCode},
		{RegisterRequest{Email: "a@gmail.com", Password: "long enough pw", EnrollmentCode: "join-the-platform"}, domain.ErrEmailNotAllowed},
		{RegisterRequest{Email: "not-an-email", Password: "long enough pw", EnrollmentCode: "join-the-platform"}, domain.ErrEmailNotAllowed},
		{RegisterRequest{Email: "a@sti-th.com", Password: "short", EnrollmentCode: "join-the-platform"}, domain.ErrWeakPassword},
		{RegisterRequest{Email: "a@sti-th.com", Password: strings.Repeat("x", 73), EnrollmentCode: "join-the-platform"}, domain.ErrWeakPassword},
	}
	for _, c := range cases {
		if _, err := fx.svc.Register(ctx, c.req); !errors.Is(err, c.want) {
			t.Errorf("%+v: got %v, want %v", c.req, err, c.want)
		}
	}
	if len(fx.repo.users) != 0 {
		t.Fatalf("a refused registration created an account: %v", fx.repo.users)
	}
	if _, err := fx.svc.Register(ctx, RegisterRequest{Email: "A@STI-TH.com", Password: "long enough pw", EnrollmentCode: "join-the-platform"}); err != nil {
		t.Fatalf("a valid registration was refused: %v", err)
	}
}

func TestRegister_StoresOnlyHashes(t *testing.T) {
	fx := newAuthFixture(t)
	issued := fx.register(t, "alice@sti-th.com")
	if !strings.HasPrefix(issued.Token, "cps_") || issued.Credential.Kind != domain.CredentialSession {
		t.Fatalf("registration should sign the employee in with a session, got %+v", issued.Credential)
	}
	for _, c := range fx.repo.creds {
		if c.hash == issued.Token || strings.Contains(c.hash, issued.Token[4:]) {
			t.Fatal("the token itself was stored")
		}
	}
	for _, u := range fx.repo.users {
		if strings.Contains(u.hash, "correct horse") || !strings.HasPrefix(u.hash, "$2") {
			t.Fatalf("the password was not stored as a bcrypt hash: %q", u.hash)
		}
	}
}

// An account the old self-declared header created has applications but no
// password. Registering claims it — same user id, so the applications stay
// theirs — and only once.
func TestRegister_ClaimsALegacyAccountOnce(t *testing.T) {
	fx := newAuthFixture(t)
	legacy := fx.repo.legacyUser("Bob@sti-th.com")

	issued := fx.register(t, "bob@sti-th.com")
	if issued.User.ID != legacy.ID {
		t.Fatalf("expected the existing account %s to be claimed, got a new one %s", legacy.ID, issued.User.ID)
	}
	if _, err := fx.svc.Register(context.Background(), RegisterRequest{
		Email: "bob@sti-th.com", Password: "someone else's pw", EnrollmentCode: "join-the-platform",
	}); !errors.Is(err, domain.ErrEmailRegistered) {
		t.Fatalf("a second registration must not take over the account, got %v", err)
	}
}

func TestLogin_SameAnswerForUnknownEmailAndWrongPassword(t *testing.T) {
	fx := newAuthFixture(t)
	fx.register(t, "alice@sti-th.com")
	fx.repo.legacyUser("nopassword@sti-th.com")
	ctx := context.Background()

	for _, email := range []string{"nobody@sti-th.com", "alice@sti-th.com", "nopassword@sti-th.com"} {
		if _, err := fx.svc.Login(ctx, email, "not the password"); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Errorf("%s: got %v, want ErrInvalidCredentials", email, err)
		}
	}
	issued, err := fx.svc.Login(ctx, "ALICE@sti-th.com", "correct horse battery")
	if err != nil || issued.Credential.Kind != domain.CredentialSession {
		t.Fatalf("expected a session, got %+v, %v", issued.Credential, err)
	}
}

func TestLogin_LocksOutAfterRepeatedFailuresThenRecovers(t *testing.T) {
	fx := newAuthFixture(t)
	fx.register(t, "alice@sti-th.com")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = fx.svc.Login(ctx, "alice@sti-th.com", "guess")
	}
	if _, err := fx.svc.Login(ctx, "alice@sti-th.com", "correct horse battery"); !errors.Is(err, domain.ErrAccountLocked) {
		t.Fatalf("even the right password must be refused while locked, got %v", err)
	}
	fx.advance(16 * time.Minute)
	if _, err := fx.svc.Login(ctx, "alice@sti-th.com", "correct horse battery"); err != nil {
		t.Fatalf("the lockout should lift after its window, got %v", err)
	}
}

func TestAuthenticate_RejectsRevokedExpiredAndForeignTokens(t *testing.T) {
	fx := newAuthFixture(t)
	issued := fx.register(t, "alice@sti-th.com")
	ctx := context.Background()

	user, cred, err := fx.svc.Authenticate(ctx, issued.Token)
	if err != nil || user.Email != "alice@sti-th.com" || cred.ID != issued.Credential.ID {
		t.Fatalf("a fresh session should authenticate, got %v / %+v", err, user)
	}
	for _, bad := range []string{"", "Bearer x", "cps_not-a-real-token", "ghp_somethingelse", issued.Token + "x"} {
		if _, _, err := fx.svc.Authenticate(ctx, bad); !errors.Is(err, domain.ErrInvalidToken) {
			t.Errorf("%q: got %v, want ErrInvalidToken", bad, err)
		}
	}

	fx.advance(13 * time.Hour) // past the 12h session lifetime
	if _, _, err := fx.svc.Authenticate(ctx, issued.Token); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("an expired session must not authenticate, got %v", err)
	}

	again, err := fx.svc.Login(ctx, "alice@sti-th.com", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Logout(ctx, again.User, again.Credential); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.svc.Authenticate(ctx, again.Token); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("a signed-out session must not authenticate, got %v", err)
	}
}

// The property the device flow exists for: a machine's key can do
// everything except manage keys and approve devices — so an agent can
// never mint itself another key or approve its own request.
func TestAPIKeys_ManagementNeedsASession(t *testing.T) {
	fx := newAuthFixture(t)
	session := fx.register(t, "alice@sti-th.com")
	ctx := context.Background()

	key, err := fx.svc.CreateAPIKey(ctx, session.User, session.Credential, "laptop-01", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key.Token, "cpk_") || key.Credential.Prefix != key.Token[:12] {
		t.Fatalf("unexpected key shape: %+v", key.Credential)
	}
	if got := key.Credential.ExpiresAt.Sub(fx.clock); got != 7*24*time.Hour {
		t.Fatalf("expected a 7-day key, got %s", got)
	}
	capped, _ := fx.svc.CreateAPIKey(ctx, session.User, session.Credential, "long", 3650)
	if got := capped.Credential.ExpiresAt.Sub(fx.clock); got != 90*24*time.Hour {
		t.Fatalf("expected the lifetime capped at 90 days, got %s", got)
	}

	_, keyCred, err := fx.svc.Authenticate(ctx, key.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.CreateAPIKey(ctx, key.User, keyCred, "another", 0); !errors.Is(err, domain.ErrSessionRequired) {
		t.Errorf("an API key minted another key: %v", err)
	}
	if _, err := fx.svc.ListAPIKeys(ctx, key.User, keyCred); !errors.Is(err, domain.ErrSessionRequired) {
		t.Errorf("an API key listed keys: %v", err)
	}
	if _, err := fx.svc.RevokeAPIKey(ctx, key.User, keyCred, key.Credential.ID); !errors.Is(err, domain.ErrSessionRequired) {
		t.Errorf("an API key revoked a key: %v", err)
	}

	if _, err := fx.svc.RevokeAPIKey(ctx, session.User, session.Credential, key.Credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.svc.Authenticate(ctx, key.Token); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("a revoked key still authenticates: %v", err)
	}
	// A session isn't an API key, even to its owner's revoke call.
	if _, err := fx.svc.RevokeAPIKey(ctx, session.User, session.Credential, session.Credential.ID); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("revoking a session through the API-key endpoint should be not-found, got %v", err)
	}
	// And nobody revokes someone else's key.
	bob := fx.register(t, "bob@sti-th.com")
	other, _ := fx.svc.CreateAPIKey(ctx, session.User, session.Credential, "alice-2", 0)
	if _, err := fx.svc.RevokeAPIKey(ctx, bob.User, bob.Credential, other.Credential.ID); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("bob revoked alice's key: %v", err)
	}
}

func TestDeviceFlow_ApproveThenCollectOnce(t *testing.T) {
	fx := newAuthFixture(t)
	alice := fx.register(t, "alice@sti-th.com")
	ctx := context.Background()

	start, err := fx.svc.StartDevice(ctx, "DESKTOP-42 (claude code)")
	if err != nil {
		t.Fatal(err)
	}
	if len(start.UserCode) != 9 || start.UserCode[4] != '-' {
		t.Fatalf("unexpected user code %q", start.UserCode)
	}
	if _, err := fx.svc.PollDevice(ctx, start.DeviceCode); !errors.Is(err, domain.ErrAuthorizationPending) {
		t.Fatalf("before approval: got %v, want pending", err)
	}

	// The employee types the code however they like.
	typed := strings.ToLower(strings.ReplaceAll(start.UserCode, "-", " "))
	pending, err := fx.svc.PendingDevice(ctx, alice.Credential, typed)
	if err != nil || pending.ClientName != "DESKTOP-42 (claude code)" {
		t.Fatalf("expected to see the pending device, got %+v, %v", pending, err)
	}

	// A machine's own key can't approve anything.
	key, _ := fx.svc.CreateAPIKey(ctx, alice.User, alice.Credential, "other machine", 0)
	_, keyCred, _ := fx.svc.Authenticate(ctx, key.Token)
	if _, err := fx.svc.DecideDevice(ctx, key.User, keyCred, start.UserCode, true); !errors.Is(err, domain.ErrSessionRequired) {
		t.Fatalf("an API key approved a device: %v", err)
	}

	if _, err := fx.svc.DecideDevice(ctx, alice.User, alice.Credential, typed, true); err != nil {
		t.Fatal(err)
	}
	issued, err := fx.svc.PollDevice(ctx, start.DeviceCode)
	if err != nil {
		t.Fatalf("after approval: %v", err)
	}
	if issued.User.ID != alice.User.ID || issued.Credential.Kind != domain.CredentialAPIKey || issued.Credential.Name != "DESKTOP-42 (claude code)" {
		t.Fatalf("expected alice's key named after the machine, got %+v", issued.Credential)
	}
	user, _, err := fx.svc.Authenticate(ctx, issued.Token)
	if err != nil || user.ID != alice.User.ID {
		t.Fatalf("the collected key should act as alice, got %v", err)
	}
	if _, err := fx.svc.PollDevice(ctx, start.DeviceCode); !errors.Is(err, domain.ErrDeviceExpired) {
		t.Fatalf("a device code must yield one key only, second poll got %v", err)
	}
	if _, err := fx.svc.PollDevice(ctx, "cpd_never-issued"); !errors.Is(err, domain.ErrDeviceExpired) {
		t.Fatalf("an unknown device code: got %v", err)
	}
}

func TestDeviceFlow_DeniedAndExpired(t *testing.T) {
	fx := newAuthFixture(t)
	alice := fx.register(t, "alice@sti-th.com")
	ctx := context.Background()

	denied, _ := fx.svc.StartDevice(ctx, "unknown box")
	if _, err := fx.svc.DecideDevice(ctx, alice.User, alice.Credential, denied.UserCode, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.PollDevice(ctx, denied.DeviceCode); !errors.Is(err, domain.ErrDeviceDenied) {
		t.Fatalf("denied: got %v", err)
	}
	if _, err := fx.svc.DecideDevice(ctx, alice.User, alice.Credential, denied.UserCode, true); !errors.Is(err, domain.ErrUserCodeNotFound) {
		t.Fatalf("a denied request must not be approvable afterwards, got %v", err)
	}

	stale, _ := fx.svc.StartDevice(ctx, "slow box")
	fx.advance(11 * time.Minute)
	if _, err := fx.svc.PollDevice(ctx, stale.DeviceCode); !errors.Is(err, domain.ErrDeviceExpired) {
		t.Fatalf("expired: got %v", err)
	}
	if _, err := fx.svc.DecideDevice(ctx, alice.User, alice.Credential, stale.UserCode, true); !errors.Is(err, domain.ErrUserCodeNotFound) {
		t.Fatalf("an expired request must not be approvable, got %v", err)
	}
}

func TestNormalizeUserCode(t *testing.T) {
	for in, want := range map[string]string{
		"BCDF-GHJK":   "BCDF-GHJK",
		"bcdfghjk":    "BCDF-GHJK",
		" bcdf ghjk ": "BCDF-GHJK",
		"bcd":         "BCD",
	} {
		if got := NormalizeUserCode(in); got != want {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", in, got, want)
		}
	}
}
