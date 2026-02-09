package main

import (
	"fmt"
	"net/http"
)

// Authenticator validates credentials and returns the canonical username to store in session.
type Authenticator interface {
	Authenticate(r *http.Request, username, password string) (string, error)
}

func NewAuthenticator(cfg *Config) (Authenticator, error) {
	switch cfg.AuthMode {
	case "local":
		return &LocalAuthenticator{cfg: cfg}, nil
	case "pam":
		return NewPAMAuthenticator(cfg)
	default:
		return nil, fmt.Errorf("unsupported auth_mode %q", cfg.AuthMode)
	}
}
