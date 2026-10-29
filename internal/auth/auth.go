package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

// Cookie names.
const (
	SessionCookie = "mn_session"
	LoginCSRF     = "mn_login_csrf"
	tokenPrefix   = "mnt_"
)

// Errors.
var (
	ErrInvalidLogin = errors.New("invalid username or password")
	ErrRateLimited  = errors.New("too many login attempts, try again in a minute")
)

// Manager handles authentication state.
type Manager struct {
	st         store.Store
	secret     []byte
	idle       time.Duration
	trustProxy bool
	now        func() time.Time
	limiter    *rateLimiter
}

// NewManager creates an auth manager.
func NewManager(st store.Store, secret string, idle time.Duration, trustProxy bool) *Manager {
	return &Manager{st: st, secret: []byte(secret), idle: idle, trustProxy: trustProxy, now: time.Now,
		limiter: newRateLimiter(5, time.Minute)}
}

// EnsureAdmin creates the single user on first run.
func (m *Manager) EnsureAdmin(ctx context.Context, username, password string) error {
	n, err := m.st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	if username == "" || password == "" {
		return errors.New("APP_ADMIN_USERNAME and APP_ADMIN_PASSWORD are required on first run")
	}
	if err := ValidateNewPassword(password); err != nil {
		return errors.New("APP_ADMIN_PASSWORD: " + err.Error())
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := m.st.CreateUser(ctx, &model.User{Username: username, PasswordHash: hash, CreatedAt: m.now().UTC()}); err != nil {
		return err
	}
	slog.Info("created admin user", "username", username)
	return nil
}

// ---- helpers ----

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (m *Manager) sign(v string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) verifySigned(c string) (string, bool) {
	v, sig, ok := strings.Cut(c, ".")
	if !ok {
		return "", false
	}
	return v, hmac.Equal([]byte(sig), []byte(m.sign(v)))
}

// ClientIP returns the client address (X-Forwarded-For only when trusted).
func (m *Manager) ClientIP(r *http.Request) string {
	if m.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(ip)
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return xr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsSecure reports whether the request arrived over HTTPS.
func (m *Manager) IsSecure(r *http.Request) bool {
	return r.TLS != nil || (m.trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

// ---- login / sessions ----

// Login verifies credentials and starts a session.
func (m *Manager) Login(ctx context.Context, w http.ResponseWriter, r *http.Request, username, password string) error {
	if !m.limiter.allow(m.ClientIP(r)) {
		return ErrRateLimited
	}
	u, err := m.st.GetUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			_, _ = VerifyPassword(dummyHash, password) // equalise timing
			return ErrInvalidLogin
		}
		return err
	}
	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil || !ok {
		return ErrInvalidLogin
	}
	return m.startSession(ctx, w, r, u.ID)
}

var dummyHash, _ = HashPassword("timing-equaliser")

func (m *Manager) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	tok := randomToken(32)
	now := m.now().UTC()
	s := &model.Session{IDHash: sha(tok), UserID: userID, CSRFToken: randomToken(24), CreatedAt: now, LastSeenAt: now}
	if err := m.st.CreateSession(ctx, s); err != nil {
		return err
	}
	_ = m.st.DeleteSessionsIdleSince(ctx, now.Add(-m.idle))
	// #nosec G124 -- Secure is set whenever the request arrived over HTTPS; plain HTTP must work on private networks.
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: tok + "." + m.sign(tok), Path: "/", HttpOnly: true,
		Secure: m.IsSecure(r), SameSite: http.SameSiteLaxMode})
	return nil
}

// Logout ends the current session.
func (m *Manager) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(ctx); s != nil {
		_ = m.st.DeleteSession(ctx, s.IDHash)
	}
	// #nosec G124 -- see startSession.
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: m.IsSecure(r), SameSite: http.SameSiteLaxMode})
}

// ChangePassword verifies the old password, stores the new one and ends other sessions.
func (m *Manager) ChangePassword(ctx context.Context, current, next string) error {
	s := SessionFrom(ctx)
	if s == nil {
		return errors.New("not logged in")
	}
	u, err := m.st.GetUser(ctx, s.UserID)
	if err != nil {
		return err
	}
	if ok, _ := VerifyPassword(u.PasswordHash, current); !ok {
		return model.Invalid("Current password is wrong")
	}
	if err := ValidateNewPassword(next); err != nil {
		return model.Invalid(err.Error())
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := m.st.UpdatePassword(ctx, u.ID, hash); err != nil {
		return err
	}
	return m.st.DeleteSessionsExcept(ctx, s.IDHash)
}

// session loads and validates the session cookie (sliding idle expiry).
func (m *Manager) session(r *http.Request) *model.Session {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil
	}
	tok, ok := m.verifySigned(c.Value)
	if !ok {
		return nil
	}
	ctx := r.Context()
	s, err := m.st.GetSession(ctx, sha(tok))
	if err != nil {
		return nil
	}
	now := m.now().UTC()
	if now.Sub(s.LastSeenAt) > m.idle {
		_ = m.st.DeleteSession(ctx, s.IDHash)
		return nil
	}
	if now.Sub(s.LastSeenAt) > time.Minute {
		_ = m.st.TouchSession(ctx, s.IDHash, now)
	}
	return s
}

// ---- context ----

type ctxKey int

const (
	sessionKey ctxKey = iota
	tokenKey
)

// SessionFrom returns the session in ctx, if any.
func SessionFrom(ctx context.Context) *model.Session {
	s, _ := ctx.Value(sessionKey).(*model.Session)
	return s
}

// CSRFToken returns the CSRF token for the current session.
func CSRFToken(ctx context.Context) string {
	if s := SessionFrom(ctx); s != nil {
		return s.CSRFToken
	}
	return ""
}

// ViaToken reports whether the request was authenticated with an API token.
func ViaToken(ctx context.Context) bool { return ctx.Value(tokenKey) != nil }

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (m *Manager) checkCSRF(r *http.Request, s *model.Session) bool {
	if safeMethod(r.Method) {
		return true
	}
	tok := r.Header.Get("X-CSRF-Token")
	if tok == "" {
		tok = r.PostFormValue("csrf_token")
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.CSRFToken)) == 1
}

// RequireWeb protects HTML routes: redirects to /login and enforces CSRF.
func (m *Manager) RequireWeb(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := m.session(r)
		if s == nil {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !m.checkCSRF(r, s) {
			http.Error(w, "invalid CSRF token, reload the page", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, s)))
	})
}

// RequireAPI protects JSON routes: Bearer token or session cookie (+CSRF header).
func (m *Manager) RequireAPI(onFail func(w http.ResponseWriter, status int, code, msg string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h := r.Header.Get("Authorization"); h != "" {
				raw, ok := strings.CutPrefix(h, "Bearer ")
				if !ok || !strings.HasPrefix(raw, tokenPrefix) {
					onFail(w, http.StatusUnauthorized, "unauthorized", "invalid authorization header")
					return
				}
				t, err := m.st.GetTokenByHash(r.Context(), sha(strings.TrimSpace(raw)))
				if err != nil || t.RevokedAt != nil {
					onFail(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked token")
					return
				}
				if t.LastUsedAt == nil || m.now().Sub(*t.LastUsedAt) > time.Minute {
					_ = m.st.TouchToken(r.Context(), t.ID, m.now().UTC())
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey, t)))
				return
			}
			s := m.session(r)
			if s == nil {
				onFail(w, http.StatusUnauthorized, "unauthorized", "login or API token required")
				return
			}
			if !safeMethod(r.Method) && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.CSRFToken)) != 1 {
				onFail(w, http.StatusForbidden, "csrf", "missing or invalid X-CSRF-Token header")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, s)))
		})
	}
}

// Username returns the logged-in user's name ("" when unknown).
func (m *Manager) Username(ctx context.Context) string {
	s := SessionFrom(ctx)
	if s == nil {
		return ""
	}
	u, err := m.st.GetUser(ctx, s.UserID)
	if err != nil {
		return ""
	}
	return u.Username
}

// IsLoggedIn reports whether the request carries a valid session.
func (m *Manager) IsLoggedIn(r *http.Request) bool { return m.session(r) != nil }

// LoginCSRFToken issues (or reuses) the pre-login double-submit token.
func (m *Manager) LoginCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(LoginCSRF); err == nil {
		if v, ok := m.verifySigned(c.Value); ok {
			return v
		}
	}
	v := randomToken(18)
	// #nosec G124 -- see startSession.
	http.SetCookie(w, &http.Cookie{Name: LoginCSRF, Value: v + "." + m.sign(v), Path: "/login", HttpOnly: true,
		Secure: m.IsSecure(r), SameSite: http.SameSiteStrictMode})
	return v
}

// CheckLoginCSRF validates the login form token against its cookie.
func (m *Manager) CheckLoginCSRF(r *http.Request) bool {
	c, err := r.Cookie(LoginCSRF)
	if err != nil {
		return false
	}
	v, ok := m.verifySigned(c.Value)
	return ok && subtle.ConstantTimeCompare([]byte(v), []byte(r.PostFormValue("csrf_token"))) == 1
}

// ---- API tokens ----

// CreateToken creates a personal API token and returns the plaintext once.
func (m *Manager) CreateToken(ctx context.Context, name string) (string, *model.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", nil, model.Invalid("Token name is required (max 100 characters)")
	}
	plain := tokenPrefix + randomToken(32)
	t := &model.APIToken{Name: name, TokenHash: sha(plain), CreatedAt: m.now().UTC()}
	if err := m.st.CreateToken(ctx, t); err != nil {
		return "", nil, err
	}
	return plain, t, nil
}

// RevokeToken revokes a token.
func (m *Manager) RevokeToken(ctx context.Context, id int64) error {
	return m.st.RevokeToken(ctx, id, m.now().UTC())
}

// ListTokens lists tokens (hashes only).
func (m *Manager) ListTokens(ctx context.Context) ([]model.APIToken, error) {
	return m.st.ListTokens(ctx)
}

// ---- rate limiting ----

type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			h = append(h, t)
		}
	}
	if len(l.hits) > 10000 { // bound memory
		l.hits = map[string][]time.Time{}
	}
	if len(h) >= l.max {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}
