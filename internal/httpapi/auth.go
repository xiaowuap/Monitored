package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/security"
	"github.com/monitored-io/monitored/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type identity struct {
	Email string `json:"email"`
	Role  string `json:"role"`
	Exp   int64  `json:"exp"`
}

type authenticator struct {
	disabled   bool
	email      string
	password   string
	key        []byte
	secure     bool
	repository *store.Repository
	sealer     *security.Sealer
}

func newAuthenticator(cfg config.Server, repositories ...*store.Repository) *authenticator {
	result := &authenticator{
		disabled: cfg.AuthDisabled,
		email:    cfg.AdminEmail,
		password: cfg.AdminPassword,
		key:      []byte(cfg.MasterKey),
		secure:   strings.HasPrefix(cfg.PublicURL, "https://"),
	}
	if len(repositories) > 0 {
		result.repository = repositories[0]
	}
	result.sealer, _ = security.NewSealer(cfg.MasterKey)
	return result
}

func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.disabled {
			next.ServeHTTP(w, withActor(r, identity{Email: a.email, Role: "admin"}))
			return
		}
		token := ""
		if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		}
		if token == "" {
			if cookie, err := r.Cookie("monitored_session"); err == nil {
				token = cookie.Value
			}
		}
		claims, err := a.verify(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Sign in is required.")
			return
		}
		next.ServeHTTP(w, withActor(r, claims))
	})
}

func requireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowed[actorFrom(r).Role]; !ok {
				writeError(w, http.StatusForbidden, "insufficient_role", "This operation is not allowed for the current role.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *authenticator) login(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	role := "admin"
	authenticated := false
	usedRepository := false
	if a.repository != nil {
		hash, storedRole, enabled, err := a.repository.UserCredential(r.Context(), payload.Email)
		if err == nil {
			usedRepository = true
			if enabled && bcrypt.CompareHashAndPassword([]byte(hash), []byte(payload.Password)) == nil {
				authenticated, role = true, storedRole
			}
		}
	}
	if !usedRepository && !authenticated {
		authenticated = subtle.ConstantTimeCompare([]byte(strings.ToLower(payload.Email)), []byte(strings.ToLower(a.email))) == 1 &&
			subtle.ConstantTimeCompare([]byte(payload.Password), []byte(a.password)) == 1
	}
	if !authenticated {
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The email or password is incorrect.")
		return
	}
	if usedRepository {
		valid, required := a.validateTOTP(r.Context(), payload.Email, payload.TOTPCode)
		if required && !valid {
			time.Sleep(250 * time.Millisecond)
			writeError(w, http.StatusUnauthorized, "totp_required", "A valid authenticator code is required.")
			return
		}
	}
	claims := identity{Email: strings.ToLower(payload.Email), Role: role, Exp: time.Now().Add(12 * time.Hour).Unix()}
	token, err := a.sign(claims)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "Unable to create a session.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "monitored_session",
		Value:    token,
		Path:     "/",
		MaxAge:   int((12 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{"email": claims.Email, "role": claims.Role},
	})
}

func (a *authenticator) validateTOTP(ctx context.Context, email, code string) (valid, required bool) {
	if a.repository == nil {
		return false, false
	}
	encrypted, enabled, err := a.repository.UserTOTPSecret(ctx, email)
	if err != nil || !enabled {
		return false, false
	}
	if a.sealer == nil {
		return false, true
	}
	secret, err := a.sealer.Open(encrypted, "totp:"+strings.ToLower(email))
	if err != nil {
		return false, true
	}
	return security.ValidateTOTP(string(secret), code, time.Now()), true
}

func (a *authenticator) reauthenticate(ctx context.Context, email, password, code string) bool {
	if a.repository == nil || a.sealer == nil {
		return false
	}
	hash, _, enabled, err := a.repository.UserCredential(ctx, email)
	if err != nil || !enabled || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false
	}
	valid, required := a.validateTOTP(ctx, email, code)
	return required && valid
}

func (a *authenticator) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "monitored_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *authenticator) me(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         map[string]string{"email": actor.Email, "role": actor.Role},
		"authDisabled": a.disabled,
	})
}

func (a *authenticator) sign(claims identity) (string, error) {
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (a *authenticator) verify(token string) (identity, error) {
	if token == "" {
		return identity{}, errors.New("missing token")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return identity{}, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, actual) {
		return identity{}, errors.New("invalid signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return identity{}, errors.New("invalid claims")
	}
	var claims identity
	if err := json.Unmarshal(body, &claims); err != nil {
		return identity{}, errors.New("invalid claims")
	}
	if claims.Exp <= time.Now().Unix() || claims.Email == "" || claims.Role == "" {
		return identity{}, errors.New("expired token")
	}
	return claims, nil
}
