package main

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"sort"
	"strings"
)

type VMNICView struct {
	Key       string
	Model     string
	MAC       string
	Bridge    string
	IPs       []string
	LeaseIP   string
	LeaseHost string
	AgentIPs  []string
}

type VMView struct {
	VMID   int
	Name   string
	Type   string
	Status string
	NICs   []VMNICView
	Agent  string
}

type UsedIP struct {
	IP     string
	Source string // "dhcp", "vm", "forward", "gateway"
	VMID   int
	VMName string
	MAC    string
}

type BridgeUsedIPs struct {
	Bridge string
	Subnet string
	IPs    []UsedIP
}

type BridgeIPOption struct {
	IP    string
	Label string
}

type BridgeIPList struct {
	Bridge  string
	Options []BridgeIPOption
}

var netKeyRe = regexp.MustCompile(`^net[0-9]+$`)

func normalizeMAC(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	v := b.String()
	if len(v) != 12 {
		return v
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", v[0:2], v[2:4], v[4:6], v[6:8], v[8:10], v[10:12])
}

func splitCommaKV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parseFirstKV(s string) (string, string) {
	// model=MAC or key=value
	i := strings.IndexByte(s, '=')
	if i <= 0 || i == len(s)-1 {
		return "", ""
	}
	return s[:i], s[i+1:]
}

func kvGet(parts []string, key string) string {
	for _, p := range parts {
		k, v := parseFirstKV(p)
		if k == key {
			return v
		}
	}
	return ""
}

func parseQemuNIC(key, val string) (VMNICView, bool) {
	if !netKeyRe.MatchString(key) {
		return VMNICView{}, false
	}
	parts := splitCommaKV(val)
	if len(parts) == 0 {
		return VMNICView{}, false
	}
	model, mac := parseFirstKV(parts[0])
	if model == "" || mac == "" {
		// qm allows "virtio,bridge=vmbr1" when setting; config usually stores model=MAC
		model = parts[0]
		mac = ""
	}
	br := kvGet(parts, "bridge")
	return VMNICView{
		Key:    key,
		Model:  model,
		MAC:    mac,
		Bridge: br,
	}, true
}

func parseLXCNIC(key, val string) (VMNICView, bool) {
	if !netKeyRe.MatchString(key) {
		return VMNICView{}, false
	}
	parts := splitCommaKV(val)
	if len(parts) == 0 {
		return VMNICView{}, false
	}
	br := kvGet(parts, "bridge")
	mac := kvGet(parts, "hwaddr")
	name := kvGet(parts, "name")
	ip := kvGet(parts, "ip")
	var ips []string
	if ip != "" && ip != "dhcp" {
		ips = append(ips, ip)
	}
	model := "lxc"
	if name != "" {
		model = name
	}
	return VMNICView{
		Key:    key,
		Model:  model,
		MAC:    mac,
		Bridge: br,
		IPs:    ips,
	}, true
}

func updateBridgeInNetString(val, newBridge string) string {
	parts := splitCommaKV(val)
	if len(parts) == 0 {
		return val
	}
	updated := false
	for i, p := range parts {
		k, _ := parseFirstKV(p)
		if k == "bridge" {
			parts[i] = "bridge=" + newBridge
			updated = true
		}
	}
	if !updated {
		parts = append(parts, "bridge="+newBridge)
	}
	return strings.Join(parts, ",")
}

func (app *App) buildBridgeNameOptions(proxmoxBridges []BridgeView) []string {
	var names []string
	for _, b := range proxmoxBridges {
		names = append(names, b.Name)
	}
	sort.Strings(names)
	return names
}

func buildVMViews(px *ProxmoxClient, vms []VM, leases []Lease) []VMView {
	leaseByMAC := make(map[string]Lease, len(leases))
	for _, l := range leases {
		m := normalizeMAC(l.MAC)
		if m == "" {
			continue
		}
		leaseByMAC[m] = l
	}

	var out []VMView
	for _, vm := range vms {
		view := VMView{VMID: vm.VMID, Name: vm.Name, Type: vm.Type, Status: vm.Status}

		cfg, err := px.GetVMConfig(vm.Type, vm.VMID)
		if err != nil {
			log.Printf("WARN: failed to get VM config %s/%d: %v", vm.Type, vm.VMID, err)
		}
		agentIPs := map[string][]string{}
		if vm.Type == "qemu" {
			ifs, err := px.GetQemuAgentInterfaces(vm.VMID)
			if err == nil {
				agentIPs = agentIPsByMAC(ifs)
				if len(agentIPs) == 0 {
					view.Agent = "no data"
				}
			} else {
				view.Agent = err.Error()
			}
		}
		for k, v := range cfg {
			if !netKeyRe.MatchString(k) {
				continue
			}
			var nic VMNICView
			var ok bool
			if vm.Type == "qemu" {
				nic, ok = parseQemuNIC(k, v)
			} else {
				nic, ok = parseLXCNIC(k, v)
			}
			if !ok {
				continue
			}
			if nic.MAC != "" {
				if l, ok := leaseByMAC[normalizeMAC(nic.MAC)]; ok {
					nic.LeaseIP = l.IP
					nic.LeaseHost = l.Hostname
					if nic.IPs == nil && l.IP != "" {
						nic.IPs = append(nic.IPs, l.IP)
					}
				}
				if ips := agentIPs[normalizeMAC(nic.MAC)]; len(ips) > 0 {
					nic.AgentIPs = appendUniqueIPs(nic.AgentIPs, ips)
					nic.IPs = appendUniqueIPs(nic.IPs, ips)
				}
			}
			view.NICs = append(view.NICs, nic)
		}
		sort.Slice(view.NICs, func(i, j int) bool { return view.NICs[i].Key < view.NICs[j].Key })
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VMID < out[j].VMID })
	return out
}

func agentIPsByMAC(ifs []AgentInterface) map[string][]string {
	out := make(map[string][]string, len(ifs))
	for _, iface := range ifs {
		mac := normalizeMAC(iface.HardwareAddress)
		if mac == "" {
			continue
		}
		for _, ip := range iface.IPAddresses {
			if ip.Address == "" {
				continue
			}
			parsed := net.ParseIP(ip.Address)
			if parsed == nil || parsed.To4() == nil {
				continue
			}
			if parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
				continue
			}
			addr := ip.Address
			if ip.Prefix > 0 {
				addr = fmt.Sprintf("%s/%d", ip.Address, ip.Prefix)
			}
			out[mac] = appendUniqueIPs(out[mac], []string{addr})
		}
	}
	return out
}

func appendUniqueIPs(dst []string, src []string) []string {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range src {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}

func buildUsedIPs(cfg *Config, leases []Lease, vms []VMView) []BridgeUsedIPs {
	bridgeBySubnet := make(map[string]*BridgeUsedIPs)
	for _, b := range cfg.Bridges {
		bridgeBySubnet[b.Name] = &BridgeUsedIPs{Bridge: b.Name, Subnet: b.Subnet}
		// Track gateway as used.
		if b.GatewayIP != "" {
			bridgeBySubnet[b.Name].IPs = append(bridgeBySubnet[b.Name].IPs, UsedIP{
				IP:     b.GatewayIP,
				Source: "gateway",
			})
		}
		// Track internal forward targets as used.
		for _, f := range b.Forwards {
			if f.IntIP == "" {
				continue
			}
			bridgeBySubnet[b.Name].IPs = append(bridgeBySubnet[b.Name].IPs, UsedIP{
				IP:     f.IntIP,
				Source: "forward",
			})
		}
	}

	// DHCP leases: assign by subnet match.
	for _, l := range leases {
		ip := net.ParseIP(l.IP).To4()
		if ip == nil {
			continue
		}
		for _, b := range cfg.Bridges {
			_, ipnet, err := net.ParseCIDR(b.Subnet)
			if err != nil {
				continue
			}
			if ipnet.Contains(ip) {
				bridgeBySubnet[b.Name].IPs = append(bridgeBySubnet[b.Name].IPs, UsedIP{
					IP:     l.IP,
					Source: "dhcp",
					MAC:    l.MAC,
				})
			}
		}
	}

	// VM IPs (best-effort: DHCP lease match + LXC static ip=).
	for _, vm := range vms {
		for _, nic := range vm.NICs {
			for _, ip := range nic.IPs {
				ip4 := net.ParseIP(strings.Split(ip, "/")[0]).To4()
				if ip4 == nil {
					continue
				}
				for _, b := range cfg.Bridges {
					_, ipnet, err := net.ParseCIDR(b.Subnet)
					if err != nil {
						continue
					}
					if ipnet.Contains(ip4) {
						bridgeBySubnet[b.Name].IPs = append(bridgeBySubnet[b.Name].IPs, UsedIP{
							IP:     strings.Split(ip, "/")[0],
							Source: "vm",
							VMID:   vm.VMID,
							VMName: vm.Name,
							MAC:    nic.MAC,
						})
					}
				}
			}
		}
	}

	var out []BridgeUsedIPs
	for _, b := range cfg.Bridges {
		entry := bridgeBySubnet[b.Name]
		if entry == nil {
			continue
		}

		seen := map[string]bool{}
		var ips []UsedIP
		for _, u := range entry.IPs {
			if u.IP == "" {
				continue
			}
			key := u.Source + "|" + u.IP
			if seen[key] {
				continue
			}
			seen[key] = true
			ips = append(ips, u)
		}
		sort.Slice(ips, func(i, j int) bool { return ips[i].IP < ips[j].IP })
		out = append(out, BridgeUsedIPs{Bridge: entry.Bridge, Subnet: entry.Subnet, IPs: ips})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bridge < out[j].Bridge })
	return out
}

func validateNetKey(key string) error {
	if !netKeyRe.MatchString(key) {
		return fmt.Errorf("invalid net key")
	}
	return nil
}

func buildBridgeIPLists(cfg *Config, vms []VMView) []BridgeIPList {
	// Only include bridges that PNAT manages (cfg.Bridges), since forwards are scoped to those subnets.
	bridgeSet := map[string]struct{}{}
	for _, b := range cfg.Bridges {
		bridgeSet[b.Name] = struct{}{}
	}

	type key struct {
		bridge string
		ip     string
	}
	seen := map[key]bool{}
	optsByBridge := map[string][]BridgeIPOption{}

	for _, vm := range vms {
		for _, nic := range vm.NICs {
			if nic.Bridge == "" {
				continue
			}
			if _, ok := bridgeSet[nic.Bridge]; !ok {
				continue
			}

			var ips []string
			if nic.LeaseIP != "" {
				ips = append(ips, nic.LeaseIP)
			}
			for _, ip := range nic.IPs {
				ip = strings.TrimSpace(ip)
				if ip == "" {
					continue
				}
				ip = strings.Split(ip, "/")[0]
				ips = append(ips, ip)
			}

			for _, ip := range ips {
				ip4 := net.ParseIP(ip).To4()
				if ip4 == nil {
					continue
				}
				k := key{bridge: nic.Bridge, ip: ip4.String()}
				if seen[k] {
					continue
				}
				seen[k] = true

				label := fmt.Sprintf("%d %s (%s)", vm.VMID, vm.Name, nic.Key)
				optsByBridge[nic.Bridge] = append(optsByBridge[nic.Bridge], BridgeIPOption{
					IP:    ip4.String(),
					Label: label,
				})
			}
		}
	}

	var out []BridgeIPList
	for bridge, opts := range optsByBridge {
		sort.Slice(opts, func(i, j int) bool { return opts[i].IP < opts[j].IP })
		out = append(out, BridgeIPList{Bridge: bridge, Options: opts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bridge < out[j].Bridge })
	return out
}
