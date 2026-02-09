package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Config is the top-level application configuration, persisted as JSON.
type Config struct {
	ListenAddr     string         `json:"listen_addr"`
	AuthMode       string         `json:"auth_mode,omitempty"`        // "local" or "pam"
	AuthPamService string         `json:"auth_pam_service,omitempty"` // PAM service name, e.g. "pnat" or "login"
	AuthAllowUsers []string       `json:"auth_allow_users,omitempty"` // optional allowlist for PAM auth
	AdminUser      string         `json:"admin_user,omitempty"`       // for local auth
	AdminPassHash  string         `json:"admin_pass_hash,omitempty"`  // for local auth (bcrypt)
	SessionSecret  string         `json:"session_secret"`
	ProxmoxURL     string         `json:"proxmox_url"`
	ProxmoxTokenID string         `json:"proxmox_token_id"`
	ProxmoxSecret  string         `json:"proxmox_secret"`
	ProxmoxNode    string         `json:"proxmox_node"`
	WanInterface   string         `json:"wan_interface"`
	Bridges        []BridgeConfig `json:"bridges"`

	mu   sync.Mutex `json:"-"`
	path string     `json:"-"`
}

// LoadConfig reads and parses a JSON config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = path
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// Save persists the config atomically (write tmp + rename).
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// Lock acquires the config mutex for mutation.
func (c *Config) Lock() { c.mu.Lock() }

// Unlock releases the config mutex.
func (c *Config) Unlock() { c.mu.Unlock() }

// FindBridge returns a pointer to the bridge with the given name, or nil.
func (c *Config) FindBridge(name string) *BridgeConfig {
	for i := range c.Bridges {
		if c.Bridges[i].Name == name {
			return &c.Bridges[i]
		}
	}
	return nil
}

// FindForward returns a pointer to the port forward with the given ID and its bridge.
func (c *Config) FindForward(id string) (*BridgeConfig, *PortForward) {
	for i := range c.Bridges {
		for j := range c.Bridges[i].Forwards {
			if c.Bridges[i].Forwards[j].ID == id {
				return &c.Bridges[i], &c.Bridges[i].Forwards[j]
			}
		}
	}
	return nil, nil
}

// DeleteForward removes a port forward by ID. Returns true if found.
func (c *Config) DeleteForward(id string) bool {
	for i := range c.Bridges {
		for j := range c.Bridges[i].Forwards {
			if c.Bridges[i].Forwards[j].ID == id {
				c.Bridges[i].Forwards = append(
					c.Bridges[i].Forwards[:j],
					c.Bridges[i].Forwards[j+1:]...,
				)
				return true
			}
		}
	}
	return false
}

// DeleteBridge removes a bridge by name. Returns true if found.
func (c *Config) DeleteBridge(name string) bool {
	for i := range c.Bridges {
		if c.Bridges[i].Name == name {
			c.Bridges = append(c.Bridges[:i], c.Bridges[i+1:]...)
			return true
		}
	}
	return false
}

func (c *Config) validate() error {
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:9090"
	}
	if c.AuthMode == "" {
		// Backwards compatible: if config has a bcrypt hash, assume local auth.
		if c.AdminPassHash != "" {
			c.AuthMode = "local"
		} else {
			c.AuthMode = "pam"
		}
	}
	switch c.AuthMode {
	case "local":
		if c.AdminUser == "" {
			return fmt.Errorf("admin_user is required for local auth")
		}
		if c.AdminPassHash == "" {
			return fmt.Errorf("admin_pass_hash is required for local auth")
		}
	case "pam":
		if c.AuthPamService == "" {
			c.AuthPamService = "pnat"
		}
	default:
		return fmt.Errorf("invalid auth_mode %q (expected \"local\" or \"pam\")", c.AuthMode)
	}
	if c.SessionSecret == "" {
		return fmt.Errorf("session_secret is required")
	}
	if c.WanInterface == "" {
		return fmt.Errorf("wan_interface is required")
	}
	for _, b := range c.Bridges {
		if b.Name == "" {
			return fmt.Errorf("bridge name is required")
		}
		if _, _, err := net.ParseCIDR(b.Subnet); err != nil {
			return fmt.Errorf("bridge %s: invalid subnet %q: %w", b.Name, b.Subnet, err)
		}
		if net.ParseIP(b.GatewayIP) == nil {
			return fmt.Errorf("bridge %s: invalid gateway_ip %q", b.Name, b.GatewayIP)
		}
		for _, f := range b.Forwards {
			if f.ExtPort == 0 || f.IntPort == 0 {
				return fmt.Errorf("bridge %s: forward ports must be > 0", b.Name)
			}
			if net.ParseIP(f.IntIP) == nil {
				return fmt.Errorf("bridge %s: invalid forward int_ip %q", b.Name, f.IntIP)
			}
		}
	}
	return nil
}

// GenerateSessionSecret creates a random 32-byte hex string.
func GenerateSessionSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// DefaultConfigPath returns the default config file location.
func DefaultConfigPath() string {
	return filepath.Join("/etc", "pnat", "pnat.json")
}
