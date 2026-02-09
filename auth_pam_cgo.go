//go:build cgo

package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/msteinert/pam"
)

type PAMAuthenticator struct {
	service string
	allow   map[string]struct{}
}

func NewPAMAuthenticator(cfg *Config) (Authenticator, error) {
	a := &PAMAuthenticator{
		service: cfg.AuthPamService,
	}
	if a.service == "" {
		a.service = "pnat"
	}
	if len(cfg.AuthAllowUsers) > 0 {
		a.allow = make(map[string]struct{}, len(cfg.AuthAllowUsers))
		for _, u := range cfg.AuthAllowUsers {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			a.allow[u] = struct{}{}
		}
	}
	return a, nil
}

func (a *PAMAuthenticator) Authenticate(_ *http.Request, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", fmt.Errorf("invalid credentials")
	}
	if a.allow != nil {
		if _, ok := a.allow[username]; !ok {
			return "", fmt.Errorf("invalid credentials")
		}
	}

	txn, err := pam.StartFunc(a.service, username, func(style pam.Style, msg string) (string, error) {
		switch style {
		case pam.PromptEchoOff, pam.PromptEchoOn:
			return password, nil
		case pam.ErrorMsg, pam.TextInfo:
			return "", nil
		default:
			return "", nil
		}
	})
	if err != nil {
		return "", fmt.Errorf("auth error")
	}

	if err := txn.Authenticate(0); err != nil {
		return "", fmt.Errorf("invalid credentials")
	}
	// Enforce account restrictions (expired, locked, etc.) if configured.
	if err := txn.AcctMgmt(0); err != nil {
		return "", fmt.Errorf("invalid credentials")
	}

	return username, nil
}
