//go:build !cgo

package main

import "fmt"

func NewPAMAuthenticator(_ *Config) (Authenticator, error) {
	return nil, fmt.Errorf("PAM auth requires cgo (build with CGO_ENABLED=1)")
}
