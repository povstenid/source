package main

import "encoding/json"

// BridgeConfig describes a managed network bridge with NAT, DHCP, and port forwarding.
type BridgeConfig struct {
	Name              string        `json:"name"`
	Subnet            string        `json:"subnet"`
	GatewayIP         string        `json:"gateway_ip"`
	NATEnabled        bool          `json:"nat_enabled"`
	HairpinNATEnabled bool          `json:"hairpin_nat_enabled"`
	DHCP              *DHCPConfig   `json:"dhcp,omitempty"`
	Forwards          []PortForward `json:"forwards,omitempty"`
}

// UnmarshalJSON keeps backward compatibility for older configs:
// if hairpin_nat_enabled is absent, default to true.
func (b *BridgeConfig) UnmarshalJSON(data []byte) error {
	type alias BridgeConfig
	var raw struct {
		alias
		HairpinNATEnabled *bool `json:"hairpin_nat_enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = BridgeConfig(raw.alias)
	if raw.HairpinNATEnabled == nil {
		b.HairpinNATEnabled = true
	} else {
		b.HairpinNATEnabled = *raw.HairpinNATEnabled
	}
	return nil
}

// DHCPConfig describes a basic DHCP pool for a bridge.
type DHCPConfig struct {
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
	LeaseTime  string `json:"lease_time"`
	DNS1       string `json:"dns1"`
	DNS2       string `json:"dns2"`
}

// PortForward describes a single DNAT rule.
type PortForward struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"` // "tcp", "udp", "tcp+udp"
	ExtPort  uint16 `json:"ext_port"`
	IntIP    string `json:"int_ip"`
	IntPort  uint16 `json:"int_port"`
	Comment  string `json:"comment"`
	Enabled  bool   `json:"enabled"`
}

// VM represents a Proxmox virtual machine or container.
type VM struct {
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type"` // "qemu" or "lxc"
}

// Lease represents a DHCP lease from dnsmasq.
type Lease struct {
	Timestamp string `json:"timestamp"`
	MAC       string `json:"mac"`
	IP        string `json:"ip"`
	Hostname  string `json:"hostname"`
}
