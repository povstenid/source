package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const (
	dnsmasqConfigPath = "/etc/pnat/dnsmasq.conf"
	dnsmasqLeaseFile  = "/var/lib/pnat/dnsmasq.leases"
	dnsmasqUnit       = "pnat-dnsmasq.service"
)

// DNSMasqManager manages dnsmasq configuration and service for DHCP.
type DNSMasqManager struct{}

func NewDNSMasqManager() *DNSMasqManager {
	return &DNSMasqManager{}
}

// Apply generates the dnsmasq config and restarts/stops the service as needed.
func (d *DNSMasqManager) Apply(cfg *Config) error {
	hasDHCP := false
	for _, b := range cfg.Bridges {
		if b.DHCP != nil {
			hasDHCP = true
			break
		}
	}

	if !hasDHCP {
		return d.stop()
	}

	config := d.generateConfig(cfg)
	if err := os.WriteFile(dnsmasqConfigPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("write dnsmasq config: %w", err)
	}

	// Restart to pick up new config
	out, err := exec.Command("systemctl", "restart", dnsmasqUnit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart dnsmasq: %w: %s", err, strings.TrimSpace(string(out)))
	}

	log.Println("dnsmasq config applied and service restarted")
	return nil
}

// Status returns whether the dnsmasq service is running.
func (d *DNSMasqManager) Status() bool {
	err := exec.Command("systemctl", "is-active", "--quiet", dnsmasqUnit).Run()
	return err == nil
}

// Leases parses the dnsmasq lease file and returns active leases.
func (d *DNSMasqManager) Leases() ([]Lease, error) {
	f, err := os.Open(dnsmasqLeaseFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var leases []Lease
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Format: timestamp MAC IP hostname clientID
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		leases = append(leases, Lease{
			Timestamp: fields[0],
			MAC:       fields[1],
			IP:        fields[2],
			Hostname:  fields[3],
		})
	}
	return leases, scanner.Err()
}

func (d *DNSMasqManager) stop() error {
	out, err := exec.Command("systemctl", "stop", dnsmasqUnit).CombinedOutput()
	if err != nil {
		s := string(out)
		// Ignore if unit not found
		if strings.Contains(s, "not loaded") || strings.Contains(s, "not found") {
			return nil
		}
		return fmt.Errorf("stop dnsmasq: %w: %s", err, strings.TrimSpace(s))
	}
	return nil
}

func (d *DNSMasqManager) generateConfig(cfg *Config) string {
	var sb strings.Builder

	sb.WriteString("# Managed by PNAT - do not edit manually\n")
	sb.WriteString("bind-interfaces\n")
	sb.WriteString("port=0\n") // DHCP only, no DNS
	sb.WriteString("keep-in-foreground\n")
	sb.WriteString("no-daemon\n")
	sb.WriteString(fmt.Sprintf("dhcp-leasefile=%s\n", dnsmasqLeaseFile))
	sb.WriteString("\n")

	for _, b := range cfg.Bridges {
		if b.DHCP == nil {
			continue
		}

		sb.WriteString(fmt.Sprintf("# Bridge %s\n", b.Name))
		sb.WriteString(fmt.Sprintf("interface=%s\n", b.Name))

		leaseTime := b.DHCP.LeaseTime
		if leaseTime == "" {
			leaseTime = "12h"
		}
		sb.WriteString(fmt.Sprintf("dhcp-range=%s,%s,%s,%s\n",
			b.Name, b.DHCP.RangeStart, b.DHCP.RangeEnd, leaseTime))

		// Gateway (option 3)
		sb.WriteString(fmt.Sprintf("dhcp-option=%s,3,%s\n", b.Name, b.GatewayIP))

		// DNS servers (option 6)
		dns := b.DHCP.DNS1
		if b.DHCP.DNS2 != "" {
			dns += "," + b.DHCP.DNS2
		}
		if dns != "" {
			sb.WriteString(fmt.Sprintf("dhcp-option=%s,6,%s\n", b.Name, dns))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
