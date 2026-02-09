package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SetupRoutes registers all HTTP routes.
func (app *App) SetupRoutes(mux *http.ServeMux) {
	// Static files
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Printf("ERROR: static FS sub: %v", err)
	} else {
		mux.Handle("/static/", http.StripPrefix("/static/",
			http.FileServer(http.FS(staticSub))))
	}

	// Public routes
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			app.HandleLoginPage(w, r)
		case http.MethodPost:
			app.HandleLoginSubmit(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// All other routes go through auth middleware
	mux.HandleFunc("/", app.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		// Route dispatch
		path := r.URL.Path

		switch {
		case path == "/" && r.Method == http.MethodGet:
			app.HandleDashboard(w, r)
		case path == "/logout" && r.Method == http.MethodPost:
			app.HandleLogout(w, r)
		case path == "/nat/toggle" && r.Method == http.MethodPost:
			app.HandleNATToggle(w, r)
		case path == "/forwards" && r.Method == http.MethodGet:
			app.HandleForwardsList(w, r)
		case path == "/forwards/add" && r.Method == http.MethodPost:
			app.HandleForwardCreate(w, r)
		case path == "/forwards/delete" && r.Method == http.MethodPost:
			app.HandleForwardDelete(w, r)
		case path == "/forwards/toggle" && r.Method == http.MethodPost:
			app.HandleForwardToggle(w, r)
		case path == "/bridges/add" && r.Method == http.MethodPost:
			app.HandleBridgeCreate(w, r)
		case path == "/bridges/attach" && r.Method == http.MethodPost:
			app.HandleBridgeAttach(w, r)
		case path == "/bridges/detach" && r.Method == http.MethodPost:
			app.HandleBridgeDetach(w, r)
		case path == "/vms/net/update" && r.Method == http.MethodPost:
			app.HandleVMNetUpdate(w, r)
		case path == "/dhcp" && r.Method == http.MethodGet:
			app.HandleDHCPList(w, r)
		case strings.HasPrefix(path, "/dhcp/edit/") && r.Method == http.MethodGet:
			app.HandleDHCPForm(w, r)
		case strings.HasPrefix(path, "/dhcp/edit/") && r.Method == http.MethodPost:
			app.HandleDHCPSave(w, r)
		case path == "/api/vms" && r.Method == http.MethodGet:
			app.HandleAPIVMs(w, r)
		case path == "/api/nft-status" && r.Method == http.MethodGet:
			app.HandleAPINFTStatus(w, r)
		case path == "/api/dhcp-leases" && r.Method == http.MethodGet:
			app.HandleAPIDHCPLeases(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

// requireAuth wraps a handler with authentication check.
func (app *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if _, ok := app.sessions.Validate(cookie.Value); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// render executes a template with the layout.
func (app *App) render(w http.ResponseWriter, name string, data map[string]any) {
	if data == nil {
		data = make(map[string]any)
	}
	if _, ok := data["LoggedIn"]; !ok {
		if name != "login.html" {
			data["LoggedIn"] = true
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := app.templates[name]
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("ERROR: render template %s: %v", name, err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// pathParam extracts the last segment from a path like /dhcp/edit/{bridge}
func pathParam(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return s
}

// --- Dashboard ---

func (app *App) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	vms, _ := app.proxmox.ListVMs()
	nftStatus, _ := app.nft.Status()
	proxmoxBridges := app.buildBridgeViews()
	uplinks := app.buildUplinkViews()
	leases, _ := app.dnsmasq.Leases()
	vmViews := buildVMViews(app.proxmox, vms, leases)
	usedIPs := buildUsedIPs(app.cfg, leases, vmViews)
	attachable := make([]BridgeView, 0, len(proxmoxBridges))
	for _, b := range proxmoxBridges {
		if !b.Managed && b.HasCIDR {
			attachable = append(attachable, b)
		}
	}

	app.render(w, "dashboard.html", map[string]any{
		"Active":            "dashboard",
		"Bridges":           app.cfg.Bridges,
		"ProxmoxBridges":    proxmoxBridges,
		"UplinkPorts":       uplinks,
		"AttachableBridges": attachable,
		"VMs":               vms,
		"VMViews":           vmViews,
		"UsedIPs":           usedIPs,
		"BridgeOptions":     app.buildBridgeNameOptions(proxmoxBridges),
		"NFTStatus":         nftStatus,
	})
}

// --- NAT Toggle ---

func (app *App) HandleNATToggle(w http.ResponseWriter, r *http.Request) {
	bridgeName := r.FormValue("bridge")

	app.cfg.Lock()
	defer app.cfg.Unlock()

	br := app.cfg.FindBridge(bridgeName)
	if br == nil {
		http.Error(w, "Bridge not found", http.StatusBadRequest)
		return
	}

	br.NATEnabled = !br.NATEnabled

	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Port Forwards ---

// ForwardView is a template-friendly struct with bridge name included.
type ForwardView struct {
	Bridge string
	PortForward
}

func (app *App) HandleForwardsList(w http.ResponseWriter, r *http.Request) {
	var forwards []ForwardView
	for _, b := range app.cfg.Bridges {
		for _, f := range b.Forwards {
			forwards = append(forwards, ForwardView{Bridge: b.Name, PortForward: f})
		}
	}

	leases, _ := app.dnsmasq.Leases()
	vms, _ := app.proxmox.ListVMs()
	vmViews := buildVMViews(app.proxmox, vms, leases)
	bridgeIPLists := buildBridgeIPLists(app.cfg, vmViews)

	app.render(w, "forwards.html", map[string]any{
		"Active":        "forwards",
		"Bridges":       app.cfg.Bridges,
		"Forwards":      forwards,
		"BridgeIPLists": bridgeIPLists,
	})
}

func (app *App) HandleForwardCreate(w http.ResponseWriter, r *http.Request) {
	bridgeName := r.FormValue("bridge")
	protocol := r.FormValue("protocol")
	extPortStr := r.FormValue("ext_port")
	intIP := r.FormValue("int_ip")
	intPortStr := r.FormValue("int_port")
	comment := r.FormValue("comment")

	extPort, err := strconv.ParseUint(extPortStr, 10, 16)
	if err != nil || extPort == 0 {
		http.Error(w, "Invalid external port", http.StatusBadRequest)
		return
	}
	intPort, err := strconv.ParseUint(intPortStr, 10, 16)
	if err != nil || intPort == 0 {
		http.Error(w, "Invalid internal port", http.StatusBadRequest)
		return
	}
	if net.ParseIP(intIP) == nil {
		http.Error(w, "Invalid internal IP", http.StatusBadRequest)
		return
	}
	if protocol != "tcp" && protocol != "udp" && protocol != "tcp+udp" {
		http.Error(w, "Invalid protocol", http.StatusBadRequest)
		return
	}
	intIPv4, err := parseIPv4(intIP)
	if err != nil {
		http.Error(w, "Invalid internal IP (IPv4 required)", http.StatusBadRequest)
		return
	}

	id := generateID()

	app.cfg.Lock()
	defer app.cfg.Unlock()

	br := app.cfg.FindBridge(bridgeName)
	if br == nil {
		http.Error(w, "Bridge not found", http.StatusBadRequest)
		return
	}
	ipnet, err := parseCIDRv4(br.Subnet)
	if err != nil {
		http.Error(w, "Bridge subnet invalid", http.StatusBadRequest)
		return
	}
	if !ipInNet(intIPv4, ipnet) {
		http.Error(w, "Internal IP not in bridge subnet", http.StatusBadRequest)
		return
	}

	// Check for duplicate external port
	for _, b := range app.cfg.Bridges {
		for _, f := range b.Forwards {
			if f.ExtPort == uint16(extPort) && f.Enabled {
				if f.Protocol == protocol || f.Protocol == "tcp+udp" || protocol == "tcp+udp" {
					http.Error(w, fmt.Sprintf("External port %d already in use", extPort), http.StatusBadRequest)
					return
				}
			}
		}
	}

	br.Forwards = append(br.Forwards, PortForward{
		ID:       id,
		Protocol: protocol,
		ExtPort:  uint16(extPort),
		IntIP:    intIP,
		IntPort:  uint16(intPort),
		Comment:  comment,
		Enabled:  true,
	})

	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}

	http.Redirect(w, r, "/forwards", http.StatusSeeOther)
}

func (app *App) HandleForwardDelete(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")

	app.cfg.Lock()
	defer app.cfg.Unlock()

	if !app.cfg.DeleteForward(id) {
		http.Error(w, "Forward not found", http.StatusBadRequest)
		return
	}

	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}

	http.Redirect(w, r, "/forwards", http.StatusSeeOther)
}

func (app *App) HandleForwardToggle(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")

	app.cfg.Lock()
	defer app.cfg.Unlock()

	_, fwd := app.cfg.FindForward(id)
	if fwd == nil {
		http.Error(w, "Forward not found", http.StatusBadRequest)
		return
	}

	fwd.Enabled = !fwd.Enabled

	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}

	http.Redirect(w, r, "/forwards", http.StatusSeeOther)
}

// --- Bridges (Proxmox API) ---

type BridgeView struct {
	Name      string
	CIDR      string
	Ports     string
	Managed   bool
	HasCIDR   bool
	Address   string
	Netmask   string
	BridgeRaw ProxmoxNetwork
}

type UplinkView struct {
	Name string
	Type string
}

var ifaceNameRe = regexp.MustCompile(`(?i)^[a-z][a-z0-9_]{1,20}([:\.]\d+)?$`)

func (app *App) buildBridgeViews() []BridgeView {
	networks, err := app.proxmox.ListNetworks()
	if err != nil {
		log.Printf("WARN: failed to list networks: %v", err)
		return nil
	}

	managed := map[string]bool{}
	for _, b := range app.cfg.Bridges {
		managed[b.Name] = true
	}

	var bridges []BridgeView
	for _, n := range networks {
		if n.Type != "bridge" {
			continue
		}
		cidr := n.CIDR
		if cidr == "" && n.Address != "" && n.Netmask != "" {
			if v, err := cidrFromAddrNetmask(n.Address, n.Netmask); err == nil {
				cidr = v
			}
		}
		bridges = append(bridges, BridgeView{
			Name:    n.Iface,
			CIDR:    cidr,
			Ports:   n.BridgePorts,
			Managed: managed[n.Iface],
			HasCIDR: cidr != "",
			Address: n.Address,
			Netmask: n.Netmask,
		})
	}

	sort.Slice(bridges, func(i, j int) bool { return bridges[i].Name < bridges[j].Name })
	return bridges
}

func (app *App) buildUplinkViews() []UplinkView {
	networks, err := app.proxmox.ListNetworks()
	if err != nil {
		log.Printf("WARN: failed to list uplinks: %v", err)
		return nil
	}
	var uplinks []UplinkView
	for _, n := range networks {
		switch n.Type {
		case "eth", "bond", "vlan":
		default:
			continue
		}
		if n.CIDR != "" || n.Address != "" {
			continue
		}
		uplinks = append(uplinks, UplinkView{Name: n.Iface, Type: n.Type})
	}
	sort.Slice(uplinks, func(i, j int) bool { return uplinks[i].Name < uplinks[j].Name })
	return uplinks
}

func (app *App) HandleBridgeCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	subnet := strings.TrimSpace(r.FormValue("subnet"))
	gateway := strings.TrimSpace(r.FormValue("gateway_ip"))
	natEnabled := r.FormValue("nat_enabled") == "1"
	bridgePorts := strings.TrimSpace(r.FormValue("bridge_ports"))
	dhcpEnabled := r.FormValue("dhcp_enabled") == "1"
	rangeStart := strings.TrimSpace(r.FormValue("range_start"))
	rangeEnd := strings.TrimSpace(r.FormValue("range_end"))
	leaseTime := strings.TrimSpace(r.FormValue("lease_time"))
	dns1 := strings.TrimSpace(r.FormValue("dns1"))
	dns2 := strings.TrimSpace(r.FormValue("dns2"))

	if name == "" {
		http.Error(w, "Bridge name is required", http.StatusBadRequest)
		return
	}
	if !ifaceNameRe.MatchString(name) {
		http.Error(w, "Invalid bridge name. Allowed: letters, цифры, '_' (без '-') и длина 2-21 символ.", http.StatusBadRequest)
		return
	}
	if subnet == "" || gateway == "" {
		http.Error(w, "Subnet and gateway IP are required", http.StatusBadRequest)
		return
	}

	cidr, err := cidrFromSubnetAndGateway(subnet, gateway)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid subnet/gateway: %v", err), http.StatusBadRequest)
		return
	}
	if normalized, err := subnetFromCIDR(subnet); err == nil {
		subnet = normalized
	}
	if dhcpEnabled {
		if rangeStart == "" || rangeEnd == "" {
			http.Error(w, "DHCP range start/end are required", http.StatusBadRequest)
			return
		}
		if err := validateDHCPRange(subnet, gateway, rangeStart, rangeEnd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if dns1 != "" {
			if _, err := parseIPv4(dns1); err != nil {
				http.Error(w, "Invalid DNS1 IP", http.StatusBadRequest)
				return
			}
		}
		if dns2 != "" {
			if _, err := parseIPv4(dns2); err != nil {
				http.Error(w, "Invalid DNS2 IP", http.StatusBadRequest)
				return
			}
		}
		if leaseTime == "" {
			leaseTime = "12h"
		}
	}

	// Ensure not already managed
	app.cfg.Lock()
	exists := app.cfg.FindBridge(name) != nil
	app.cfg.Unlock()
	if exists {
		http.Error(w, "Bridge already managed by PNAT", http.StatusBadRequest)
		return
	}

	if bridgePorts != "" {
		uplinks := app.buildUplinkViews()
		allowed := false
		for _, u := range uplinks {
			if u.Name == bridgePorts {
				allowed = true
				break
			}
		}
		if !allowed {
			http.Error(w, "Invalid bridge port (not available)", http.StatusBadRequest)
			return
		}
	}

	// Create bridge via Proxmox API
	if err := app.proxmox.CreateBridge(name, cidr, bridgePorts); err != nil {
		http.Error(w, fmt.Sprintf("Proxmox API error: %v", err), http.StatusBadRequest)
		return
	}
	if err := app.proxmox.ReloadNetwork(); err != nil {
		http.Error(w, fmt.Sprintf("Proxmox reload error: %v", err), http.StatusBadRequest)
		return
	}

	app.cfg.Lock()
	br := BridgeConfig{
		Name:       name,
		Subnet:     subnet,
		GatewayIP:  gateway,
		NATEnabled: natEnabled,
	}
	if dhcpEnabled {
		br.DHCP = &DHCPConfig{
			RangeStart: rangeStart,
			RangeEnd:   rangeEnd,
			LeaseTime:  leaseTime,
			DNS1:       dns1,
			DNS2:       dns2,
		}
	}
	app.cfg.Bridges = append(app.cfg.Bridges, br)
	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}
	if err := app.dnsmasq.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply dnsmasq: %v", err)
	}
	app.cfg.Unlock()

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *App) HandleBridgeAttach(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Bridge name is required", http.StatusBadRequest)
		return
	}
	if !ifaceNameRe.MatchString(name) {
		http.Error(w, "Invalid bridge name", http.StatusBadRequest)
		return
	}
	natEnabled := r.FormValue("nat_enabled") == "1"
	dhcpEnabled := r.FormValue("dhcp_enabled") == "1"
	rangeStart := strings.TrimSpace(r.FormValue("range_start"))
	rangeEnd := strings.TrimSpace(r.FormValue("range_end"))
	leaseTime := strings.TrimSpace(r.FormValue("lease_time"))
	dns1 := strings.TrimSpace(r.FormValue("dns1"))
	dns2 := strings.TrimSpace(r.FormValue("dns2"))

	// Find bridge in Proxmox network config
	networks, err := app.proxmox.ListNetworks()
	if err != nil {
		http.Error(w, fmt.Sprintf("Proxmox API error: %v", err), http.StatusBadRequest)
		return
	}

	var cidr string
	for _, n := range networks {
		if n.Iface != name || n.Type != "bridge" {
			continue
		}
		if n.CIDR != "" {
			cidr = n.CIDR
		} else if n.Address != "" && n.Netmask != "" {
			c, err := cidrFromAddrNetmask(n.Address, n.Netmask)
			if err == nil {
				cidr = c
			}
		}
		break
	}
	if cidr == "" {
		http.Error(w, "Bridge has no IP/CIDR configured in Proxmox", http.StatusBadRequest)
		return
	}

	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		http.Error(w, "Invalid bridge CIDR", http.StatusBadRequest)
		return
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		http.Error(w, "IPv4 CIDR required for bridge", http.StatusBadRequest)
		return
	}
	ones, _ := ipnet.Mask.Size()
	subnet := fmt.Sprintf("%s/%d", ipv4.Mask(ipnet.Mask).String(), ones)

	if dhcpEnabled {
		if rangeStart == "" || rangeEnd == "" {
			http.Error(w, "DHCP range start/end are required", http.StatusBadRequest)
			return
		}
		if err := validateDHCPRange(subnet, ipv4.String(), rangeStart, rangeEnd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if dns1 != "" {
			if _, err := parseIPv4(dns1); err != nil {
				http.Error(w, "Invalid DNS1 IP", http.StatusBadRequest)
				return
			}
		}
		if dns2 != "" {
			if _, err := parseIPv4(dns2); err != nil {
				http.Error(w, "Invalid DNS2 IP", http.StatusBadRequest)
				return
			}
		}
		if leaseTime == "" {
			leaseTime = "12h"
		}
	}

	app.cfg.Lock()
	defer app.cfg.Unlock()
	if app.cfg.FindBridge(name) != nil {
		http.Error(w, "Bridge already managed by PNAT", http.StatusBadRequest)
		return
	}
	br := BridgeConfig{
		Name:       name,
		Subnet:     subnet,
		GatewayIP:  ipv4.String(),
		NATEnabled: natEnabled,
	}
	if dhcpEnabled {
		br.DHCP = &DHCPConfig{
			RangeStart: rangeStart,
			RangeEnd:   rangeEnd,
			LeaseTime:  leaseTime,
			DNS1:       dns1,
			DNS2:       dns2,
		}
	}
	app.cfg.Bridges = append(app.cfg.Bridges, br)
	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}
	if err := app.dnsmasq.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply dnsmasq: %v", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *App) HandleBridgeDetach(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Bridge name is required", http.StatusBadRequest)
		return
	}

	app.cfg.Lock()
	defer app.cfg.Unlock()

	if !app.cfg.DeleteBridge(name) {
		http.Error(w, "Bridge not managed by PNAT", http.StatusBadRequest)
		return
	}
	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.nft.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply nftables: %v", err)
	}
	if err := app.dnsmasq.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply dnsmasq: %v", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- VM Networks (Proxmox API) ---

func (app *App) HandleVMNetUpdate(w http.ResponseWriter, r *http.Request) {
	vmidStr := strings.TrimSpace(r.FormValue("vmid"))
	vmType := strings.TrimSpace(r.FormValue("type"))
	netKey := strings.TrimSpace(r.FormValue("net"))
	newBridge := strings.TrimSpace(r.FormValue("bridge"))

	vmid, err := strconv.Atoi(vmidStr)
	if err != nil || vmid <= 0 {
		http.Error(w, "Invalid VMID", http.StatusBadRequest)
		return
	}
	if vmType != "qemu" && vmType != "lxc" {
		http.Error(w, "Invalid VM type", http.StatusBadRequest)
		return
	}
	if !ifaceNameRe.MatchString(newBridge) {
		http.Error(w, "Invalid bridge name", http.StatusBadRequest)
		return
	}

	// Default to net0 if not provided.
	if netKey == "" {
		netKey = "net0"
	}
	if err := validateNetKey(netKey); err != nil {
		http.Error(w, "Invalid net key", http.StatusBadRequest)
		return
	}

	cfg, err := app.proxmox.GetVMConfig(vmType, vmid)
	if err != nil {
		http.Error(w, fmt.Sprintf("Proxmox API error: %v", err), http.StatusBadRequest)
		return
	}
	cur := cfg[netKey]

	var next string
	if cur == "" {
		// Add a default NIC only for QEMU.
		if vmType != "qemu" {
			http.Error(w, "LXC net add is not supported here (create net in CT config first)", http.StatusBadRequest)
			return
		}
		// Proxmox accepts: "virtio,bridge=vmbrX" and will generate a MAC.
		next = fmt.Sprintf("virtio,bridge=%s", newBridge)
	} else {
		next = updateBridgeInNetString(cur, newBridge)
	}

	values := url.Values{}
	values.Set(netKey, next)
	if err := app.proxmox.SetVMConfig(vmType, vmid, values); err != nil {
		http.Error(w, fmt.Sprintf("Proxmox API error: %v", err), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- DHCP ---

func (app *App) HandleDHCPList(w http.ResponseWriter, r *http.Request) {
	leases, _ := app.dnsmasq.Leases()

	app.render(w, "dhcp.html", map[string]any{
		"Active":  "dhcp",
		"Bridges": app.cfg.Bridges,
		"Leases":  leases,
	})
}

func (app *App) HandleDHCPForm(w http.ResponseWriter, r *http.Request) {
	bridgeName := pathParam(r.URL.Path, "/dhcp/edit/")

	br := app.cfg.FindBridge(bridgeName)
	if br == nil {
		http.Error(w, "Bridge not found", http.StatusNotFound)
		return
	}

	data := map[string]any{
		"Active":     "dhcp",
		"BridgeName": br.Name,
		"GatewayIP":  br.GatewayIP,
		"Enabled":    false,
		"RangeStart": "",
		"RangeEnd":   "",
		"LeaseTime":  "12h",
		"DNS1":       "1.1.1.1",
		"DNS2":       "8.8.8.8",
	}

	if br.DHCP != nil {
		data["Enabled"] = true
		data["RangeStart"] = br.DHCP.RangeStart
		data["RangeEnd"] = br.DHCP.RangeEnd
		data["LeaseTime"] = br.DHCP.LeaseTime
		data["DNS1"] = br.DHCP.DNS1
		data["DNS2"] = br.DHCP.DNS2
	}

	app.render(w, "dhcp_form.html", data)
}

func (app *App) HandleDHCPSave(w http.ResponseWriter, r *http.Request) {
	bridgeName := pathParam(r.URL.Path, "/dhcp/edit/")
	enabled := r.FormValue("enabled") == "1"
	rangeStart := r.FormValue("range_start")
	rangeEnd := r.FormValue("range_end")
	leaseTime := r.FormValue("lease_time")
	dns1 := r.FormValue("dns1")
	dns2 := r.FormValue("dns2")

	app.cfg.Lock()
	defer app.cfg.Unlock()

	br := app.cfg.FindBridge(bridgeName)
	if br == nil {
		http.Error(w, "Bridge not found", http.StatusNotFound)
		return
	}

	if !enabled {
		br.DHCP = nil
	} else {
		if err := validateDHCPRange(br.Subnet, br.GatewayIP, rangeStart, rangeEnd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if dns1 != "" {
			if _, err := parseIPv4(dns1); err != nil {
				http.Error(w, "Invalid DNS1 IP", http.StatusBadRequest)
				return
			}
		}
		if dns2 != "" {
			if _, err := parseIPv4(dns2); err != nil {
				http.Error(w, "Invalid DNS2 IP", http.StatusBadRequest)
				return
			}
		}
		if leaseTime == "" {
			leaseTime = "12h"
		}

		br.DHCP = &DHCPConfig{
			RangeStart: rangeStart,
			RangeEnd:   rangeEnd,
			LeaseTime:  leaseTime,
			DNS1:       dns1,
			DNS2:       dns2,
		}
	}

	if err := app.cfg.Save(); err != nil {
		log.Printf("ERROR: save config: %v", err)
	}
	if err := app.dnsmasq.Apply(app.cfg); err != nil {
		log.Printf("ERROR: apply dnsmasq: %v", err)
	}

	http.Redirect(w, r, "/dhcp", http.StatusSeeOther)
}

// --- API endpoints (JSON) ---

func (app *App) HandleAPIVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := app.proxmox.ListVMs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, vms)
}

func (app *App) HandleAPINFTStatus(w http.ResponseWriter, r *http.Request) {
	status, err := app.nft.Status()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rules": status})
}

func (app *App) HandleAPIDHCPLeases(w http.ResponseWriter, r *http.Request) {
	leases, err := app.dnsmasq.Leases()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, leases)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
