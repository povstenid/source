package main

import (
	"fmt"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

type LocalAuthenticator struct {
	cfg *Config
}

func (a *LocalAuthenticator) Authenticate(_ *http.Request, username, password string) (string, error) {
	if username != a.cfg.AdminUser {
		return "", fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.cfg.AdminPassHash), []byte(password)); err != nil {
		return "", fmt.Errorf("invalid credentials")
	}
	return username, nil
}
