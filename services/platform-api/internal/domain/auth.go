// Auth implements the platform's own authentication (DEC-001/DEC-003 as
// revised 2026-10-23, docs/17_Decision_Log.md): employees sign in with an
// email and password, and every Claude Code machine fetches its own API
// key — through a device authorization the employee approves while signed
// in — before it can deploy anything.
//
// Two kinds of credential, one table:
//
//   - a **session** is what a password sign-in gets: short-lived, used by
//     the Admin Portal, and the only kind that may manage credentials or
//     approve a device. An agent therefore can never approve itself or
//     mint itself another key.
//   - an **API key** is what a machine gets: longer-lived, named after the
//     machine, revocable from the Portal, usable for everything else.
//
// Only a SHA-256 of each token is stored. Tokens are 256 random bits, so a
// fast hash is enough — there is nothing to brute-force — and a copy of the
// platform database yields no usable credential.
package domain

import (
	"errors"
	"time"
)

type CredentialKind string

const (
	CredentialSession CredentialKind = "session"
	CredentialAPIKey  CredentialKind = "api_key"
)

// Credential is one session or API key, never its token.
type Credential struct {
	ID     string
	UserID string
	Kind   CredentialKind
	// Name is what the employee or the machine called it, e.g. the
	// hostname of the machine a key was issued to.
	Name string
	// Prefix is the token's first characters — enough to recognise a key
	// in a list, nowhere near enough to use it.
	Prefix     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Active reports whether the credential can still authenticate at now.
func (c Credential) Active(now time.Time) bool {
	return c.RevokedAt == nil && now.Before(c.ExpiresAt)
}

type DeviceStatus string

const (
	DevicePending  DeviceStatus = "pending"
	DeviceApproved DeviceStatus = "approved"
	DeviceDenied   DeviceStatus = "denied"
	// DeviceConsumed: the approved key has been handed to the machine. A
	// device code works exactly once.
	DeviceConsumed DeviceStatus = "consumed"
)

// DeviceAuthorization is one machine asking for an API key (the OAuth 2.0
// device authorization grant's shape, RFC 8628): it shows the employee a
// short user code, the employee approves it in the Portal while signed in,
// and the machine — polling with a device code only it holds — receives
// the key. The key never passes through the employee's screen or the AI
// agent's transcript.
type DeviceAuthorization struct {
	ID         string
	UserCode   string
	ClientName string
	Status     DeviceStatus
	// UserID is who approved or denied it; empty while pending.
	UserID       string
	CredentialID string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	DecidedAt    *time.Time
}

var (
	// ErrInvalidCredentials is deliberately the same answer for an unknown
	// email, a wrong password and an account with no password yet, so
	// sign-in can't be used to find out who has an account.
	ErrInvalidCredentials = errors.New("email or password is incorrect")
	ErrAccountLocked      = errors.New("too many failed sign-in attempts; try again later")
	ErrEmailRegistered    = errors.New("an account with this email already exists; sign in instead")
	ErrEnrollmentCode     = errors.New("the enrollment code is not correct")
	ErrWeakPassword       = errors.New("password must be 10 to 72 characters")
	ErrEmailNotAllowed    = errors.New("this email address is not allowed to register")
	ErrInvalidToken       = errors.New("missing, unknown, expired or revoked credential")
	ErrSessionRequired    = errors.New("this action needs a signed-in session, not an API key")
	ErrCredentialNotFound = errors.New("no such credential")

	ErrAuthorizationPending = errors.New("the employee has not approved this device yet")
	ErrDeviceDenied         = errors.New("the employee denied this device")
	ErrDeviceExpired        = errors.New("this device code is unknown, expired or already used")
	ErrUserCodeNotFound     = errors.New("no pending device with this code")
)
