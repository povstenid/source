package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const (
	nftBinary  = "/usr/sbin/nft"
	rulesDir   = "/run/pnat"
	rulesFile  = "/run/pnat/rules.nft"
	sysctlFile = "/etc/sysctl.d/90-pnat.conf"
	sysctlProc = "/proc/sys/net/ipv4/ip_forward"
	nftTable   = "ip pnat"
)

// NFTManager manages nftables rules for NAT and port forwarding.
type NFTManager struct{}

func NewNFTManager() *NFTManager {
	return &NFTManager{}
}

// Apply generates and atomically applies nftables rules from config.
func (n *NFTManager) Apply(cfg *Config) error {
	hasNAT := false
	hasRules := false

	for _, b := range cfg.Bridges {
		if b.NATEnabled {
			hasNAT = true
			hasRules = true
		}
		for _, f := range b.Forwards {
			if f.Enabled {
				hasRules = true
			}
		}
	}

	// Enable IP forwarding if any NAT is active
	if hasNAT {
		if err := enableIPForward(); err != nil {
			log.Printf("WARN: failed to enable ip_forward: %v", err)
		}
	}

	if !hasRules {
		return n.Remove()
	}

	rules := n.generateRuleset(cfg)

	// Ensure runtime directory exists
	os.MkdirAll(rulesDir, 0755)

	// Write rules atomically
	tmp := rulesFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(rules), 0644); err != nil {
		return fmt.Errorf("write rules: %w", err)
	}
	if err := os.Rename(tmp, rulesFile); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename rules: %w", err)
	}

	// Apply with nft -f
	out, err := exec.Command(nftBinary, "-f", rulesFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft apply: %w: %s", err, strings.TrimSpace(string(out)))
	}

	log.Println("nftables rules applied successfully")
	return nil
}

// Remove deletes the pnat nftables table entirely.
func (n *NFTManager) Remove() error {
	out, err := exec.Command(nftBinary, "delete", "table", "ip", "pnat").CombinedOutput()
	if err != nil {
		s := string(out)
		// Ignore "No such file or directory" — table doesn't exist
		if strings.Contains(s, "No such file or directory") || strings.Contains(s, "does not exist") {
			return nil
		}
		return fmt.Errorf("nft delete table: %w: %s", err, strings.TrimSpace(s))
	}
	log.Println("nftables table removed")
	return nil
}

// Status returns the current nftables rules for the pnat table.
func (n *NFTManager) Status() (string, error) {
	out, err := exec.Command(nftBinary, "list", "table", "ip", "pnat").CombinedOutput()
	if err != nil {
		s := string(out)
		if strings.Contains(s, "No such file or directory") || strings.Contains(s, "does not exist") {
			return "(no rules loaded)", nil
		}
		return "", fmt.Errorf("nft list: %w: %s", err, strings.TrimSpace(s))
	}
	return string(out), nil
}

func (n *NFTManager) generateRuleset(cfg *Config) string {
	var sb strings.Builder

	sb.WriteString("# Managed by PNAT - do not edit manually\n")
	sb.WriteString("add table ip pnat\n")
	sb.WriteString("flush table ip pnat\n\n")
	sb.WriteString("table ip pnat {\n")

	// Prerouting chain: DNAT rules for port forwards
	sb.WriteString("    chain prerouting {\n")
	sb.WriteString("        type nat hook prerouting priority dstnat; policy accept;\n")

	for _, b := range cfg.Bridges {
		for _, f := range b.Forwards {
			if !f.Enabled {
				continue
			}
			comment := ""
			if f.Comment != "" {
				comment = fmt.Sprintf(" comment %q", f.Comment)
			}

			protocols := []string{f.Protocol}
			if f.Protocol == "tcp+udp" {
				protocols = []string{"tcp", "udp"}
			}

			for _, proto := range protocols {
				sb.WriteString(fmt.Sprintf(
					"        iifname %q %s dport %d dnat to %s:%d%s\n",
					cfg.WanInterface, proto, f.ExtPort, f.IntIP, f.IntPort, comment,
				))
			}
		}
	}
	sb.WriteString("    }\n\n")

	// Postrouting chain: masquerade for NAT-enabled bridges
	sb.WriteString("    chain postrouting {\n")
	sb.WriteString("        type nat hook postrouting priority srcnat; policy accept;\n")

	for _, b := range cfg.Bridges {
		if !b.NATEnabled {
			continue
		}
		sb.WriteString(fmt.Sprintf(
			"        oifname %q ip saddr %s masquerade\n",
			cfg.WanInterface, b.Subnet,
		))
	}
	sb.WriteString("    }\n")
	sb.WriteString("}\n")

	return sb.String()
}

func enableIPForward() error {
	// Set immediately
	if err := os.WriteFile(sysctlProc, []byte("1"), 0644); err != nil {
		return fmt.Errorf("write ip_forward: %w", err)
	}
	// Persist across reboots
	content := "# Managed by PNAT\nnet.ipv4.ip_forward = 1\n"
	if err := os.WriteFile(sysctlFile, []byte(content), 0644); err != nil {
		log.Printf("WARN: failed to persist sysctl: %v", err)
	}
	return nil
}
