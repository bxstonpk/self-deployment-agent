// Endpoints (DEC-001/DEC-003 revised 2026-10-23 — see internal/domain/auth.go):
//
//	POST   /auth/register                  enrollment code + email + password -> session
//	POST   /auth/login                     email + password -> session
//	POST   /auth/device                    a machine asks for an API key
//	POST   /auth/device/token              the machine collects it once approved
//
//	GET    /auth/me                        who this credential belongs to
//	POST   /auth/logout                    revoke the credential used
//
//	session only (a person signed in to the Portal, never an API key):
//	GET    /auth/api-keys                  list my keys
//	POST   /auth/api-keys                  issue one from the Portal
//	DELETE /auth/api-keys/{id}             revoke one
//	GET    /auth/device/{userCode}         what am I about to approve?
//	POST   /auth/device/{userCode}/approve
//	POST   /auth/device/{userCode}/deny
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type AuthHandler struct {
	svc *service.AuthService
	// portalURL is where an employee approves a device — the Admin
	// Portal's own address, which the API can't derive.
	portalURL string
	// isAdmin says whether an account is a platform administrator, so the
	// Portal knows whether to offer the dashboard. The API checks again.
	isAdmin func(email string) bool
	// isApprover says whether an account holds an approver key
	// (PLATFORM_APPROVER_KEYS), so the Portal offers the Approvals page.
	isApprover func(email string) bool
}

func NewAuthHandler(svc *service.AuthService, portalURL string, isAdmin, isApprover func(email string) bool) *AuthHandler {
	return &AuthHandler{svc: svc, portalURL: portalURL, isAdmin: isAdmin, isApprover: isApprover}
}

type authUserResponse struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	FullName     string `json:"full_name"`
	DepartmentID string `json:"department_id"`
}

func toAuthUser(u domain.User) authUserResponse {
	return authUserResponse{ID: u.ID, Email: u.Email, FullName: u.FullName, DepartmentID: u.DepartmentID}
}

type credentialResponse struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  string  `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
	Active     bool    `json:"active"`
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toCredential(c domain.Credential) credentialResponse {
	return credentialResponse{
		ID: c.ID, Kind: string(c.Kind), Name: c.Name, Prefix: c.Prefix,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: c.ExpiresAt.UTC().Format(time.RFC3339),
		LastUsedAt: formatTimePtr(c.LastUsedAt), RevokedAt: formatTimePtr(c.RevokedAt),
		Active: c.Active(time.Now()),
	}
}

func decodeAuthBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
		return false
	}
	return true
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case errors.Is(err, domain.ErrAccountLocked):
		writeError(w, http.StatusTooManyRequests, "account_locked", err.Error())
	case errors.Is(err, domain.ErrEmailRegistered):
		writeError(w, http.StatusConflict, "email_registered", err.Error())
	case errors.Is(err, domain.ErrEnrollmentCode):
		writeError(w, http.StatusForbidden, "invalid_enrollment_code", err.Error())
	case errors.Is(err, domain.ErrEmailNotAllowed):
		writeError(w, http.StatusForbidden, "email_not_allowed", err.Error())
	case errors.Is(err, domain.ErrInvalidDepartmentName):
		writeError(w, http.StatusBadRequest, "invalid_department_name", err.Error())
	case errors.Is(err, domain.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
	case errors.Is(err, domain.ErrSessionRequired):
		writeError(w, http.StatusForbidden, "session_required", err.Error())
	case errors.Is(err, domain.ErrCredentialNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrUserCodeNotFound):
		writeError(w, http.StatusNotFound, "user_code_not_found", err.Error())
	// RFC 8628 §3.5's error codes, so any device-flow client understands them.
	case errors.Is(err, domain.ErrAuthorizationPending):
		writeError(w, http.StatusBadRequest, "authorization_pending", err.Error())
	case errors.Is(err, domain.ErrDeviceDenied):
		writeError(w, http.StatusBadRequest, "access_denied", err.Error())
	case errors.Is(err, domain.ErrDeviceExpired):
		writeError(w, http.StatusBadRequest, "expired_token", err.Error())
	case errors.Is(err, domain.ErrInvalidToken):
		writeUnauthorized(w, err)
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed unexpectedly")
	}
}

type sessionResponse struct {
	Token     string           `json:"token"`
	ExpiresAt string           `json:"expires_at"`
	User      authUserResponse `json:"user"`
}

func toSession(issued service.IssuedCredential) sessionResponse {
	return sessionResponse{
		Token: issued.Token, ExpiresAt: issued.Credential.ExpiresAt.UTC().Format(time.RFC3339), User: toAuthUser(issued.User),
	}
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email          string `json:"email"`
		Password       string `json:"password"`
		FullName       string `json:"full_name"`
		Department     string `json:"department"`
		EnrollmentCode string `json:"enrollment_code"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	issued, err := h.svc.Register(r.Context(), service.RegisterRequest{
		Email: body.Email, Password: body.Password, FullName: body.FullName,
		Department: body.Department, EnrollmentCode: body.EnrollmentCode,
	})
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSession(issued))
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	issued, err := h.svc.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSession(issued))
}

func (h *AuthHandler) StartDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName string `json:"client_name"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	start, err := h.svc.StartDevice(r.Context(), body.ClientName)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	verify := h.portalURL + "/device"
	writeJSON(w, http.StatusCreated, map[string]any{
		"device_code":               start.DeviceCode,
		"user_code":                 start.UserCode,
		"verification_uri":          verify,
		"verification_uri_complete": verify + "?code=" + url.QueryEscape(start.UserCode),
		"expires_in":                int(start.ExpiresIn.Seconds()),
		"interval":                  int(start.Interval.Seconds()),
	})
}

func (h *AuthHandler) DeviceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	issued, err := h.svc.PollDevice(r.Context(), body.DeviceCode)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":    issued.Token,
		"credential": toCredential(issued.Credential),
		"user":       toAuthUser(issued.User),
	})
}

// caller returns the authenticated user and credential, or answers 401.
func caller(w http.ResponseWriter, r *http.Request) (domain.User, domain.Credential, bool) {
	user, ok := UserFromContext(r.Context())
	cred, credOK := CredentialFromContext(r.Context())
	if !ok || !credOK {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return domain.User{}, domain.Credential{}, false
	}
	return user, cred, true
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, cred, ok := caller(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": toAuthUser(user), "credential": toCredential(cred),
		"is_admin":    h.isAdmin != nil && h.isAdmin(user.Email),
		"is_approver": h.isApprover != nil && h.isApprover(user.Email),
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	user, cred, ok := caller(w, r)
	if !ok {
		return
	}
	if err := h.svc.Logout(r.Context(), user, cred); err != nil {
		writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user, cred, ok := caller(w, r)
	if !ok {
		return
	}
	keys, err := h.svc.ListAPIKeys(r.Context(), user, cred)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	out := make([]credentialResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, toCredential(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": out})
}

func (h *AuthHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	user, cred, ok := caller(w, r)
	if !ok {
		return
	}
	var body struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	issued, err := h.svc.CreateAPIKey(r.Context(), user, cred, body.Name, body.ExpiresInDays)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"api_key": issued.Token, "credential": toCredential(issued.Credential)})
}

func (h *AuthHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	user, cred, ok := caller(w, r)
	if !ok {
		return
	}
	revoked, err := h.svc.RevokeAPIKey(r.Context(), user, cred, chi.URLParam(r, "id"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credential": toCredential(revoked)})
}

type deviceResponse struct {
	UserCode   string `json:"user_code"`
	ClientName string `json:"client_name"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
}

func toDevice(d domain.DeviceAuthorization) deviceResponse {
	return deviceResponse{
		UserCode: d.UserCode, ClientName: d.ClientName, Status: string(d.Status),
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: d.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

func (h *AuthHandler) GetDevice(w http.ResponseWriter, r *http.Request) {
	_, cred, ok := caller(w, r)
	if !ok {
		return
	}
	device, err := h.svc.PendingDevice(r.Context(), cred, chi.URLParam(r, "userCode"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": toDevice(device)})
}

func (h *AuthHandler) decideDevice(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, cred, ok := caller(w, r)
		if !ok {
			return
		}
		device, err := h.svc.DecideDevice(r.Context(), user, cred, chi.URLParam(r, "userCode"), approve)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device": toDevice(device)})
	}
}

func (h *AuthHandler) ApproveDevice(w http.ResponseWriter, r *http.Request) {
	h.decideDevice(true)(w, r)
}

func (h *AuthHandler) DenyDevice(w http.ResponseWriter, r *http.Request) {
	h.decideDevice(false)(w, r)
}
