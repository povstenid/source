package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookie = "pnat_session"
	sessionMaxAge = 24 * time.Hour
	cleanInterval = 1 * time.Hour
)

// Session represents an authenticated user session.
type Session struct {
	User      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore manages in-memory sessions.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewSessionStore creates a session store and starts the cleanup goroutine.
func NewSessionStore(secret string) *SessionStore {
	s := &SessionStore{
		sessions: make(map[string]*Session),
	}
	go s.cleanLoop()
	return s
}

// Create creates a new session and returns its token.
func (s *SessionStore) Create(user string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.sessions[token] = &Session{
		User:      user,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionMaxAge),
	}
	return token, nil
}

// Validate checks if a token is valid and not expired.
func (s *SessionStore) Validate(token string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(s.sessions, token)
		return nil, false
	}
	return sess, true
}

// Delete removes a session.
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

func (s *SessionStore) cleanLoop() {
	ticker := time.NewTicker(cleanInterval)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, v := range s.sessions {
			if now.After(v.ExpiresAt) {
				delete(s.sessions, k)
			}
		}
		s.mu.Unlock()
	}
}

// AuthMiddleware redirects unauthenticated users to /login.
func (app *App) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if _, ok := app.sessions.Validate(cookie.Value); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HandleLoginPage renders the login form.
func (app *App) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, "login.html", nil)
}

// HandleLoginSubmit processes login form submission.
func (app *App) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	user := r.FormValue("username")
	pass := r.FormValue("password")

	canonUser, err := app.auth.Authenticate(r, user, pass)
	if err != nil {
		app.render(w, "login.html", map[string]any{"Error": "Invalid credentials"})
		return
	}

	token, err := app.sessions.Create(canonUser)
	if err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// HandleLogout destroys the session and redirects to login.
func (app *App) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		app.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   sessionCookie,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
