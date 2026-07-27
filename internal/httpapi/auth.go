package httpapi

import (
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
)

type identity struct {
	Email string `json:"email"`
	Role  string `json:"role"`
	Exp   int64  `json:"exp"`
}

type authenticator struct {
	disabled bool
	email    string
	password string
	key      []byte
	secure   bool
}

func newAuthenticator(cfg config.Server) *authenticator {
	return &authenticator{
		disabled: cfg.AuthDisabled,
		email:    cfg.AdminEmail,
		password: cfg.AdminPassword,
		key:      []byte(cfg.MasterKey),
		secure:   strings.HasPrefix(cfg.PublicURL, "https://"),
	}
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

func (a *authenticator) login(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	emailMatch := subtle.ConstantTimeCompare([]byte(strings.ToLower(payload.Email)), []byte(strings.ToLower(a.email)))
	passwordMatch := subtle.ConstantTimeCompare([]byte(payload.Password), []byte(a.password))
	if emailMatch != 1 || passwordMatch != 1 {
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The email or password is incorrect.")
		return
	}
	claims := identity{Email: a.email, Role: "admin", Exp: time.Now().Add(12 * time.Hour).Unix()}
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
