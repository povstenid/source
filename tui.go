package main

import (
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type TUIMode struct {
	cfgPath string

	app     *tview.Application
	pages   *tview.Pages
	header  *tview.TextView
	footer  *tview.TextView
	tabName string
	focus   map[string]tview.Primitive

	cfg          *Config
	leases       []Lease
	vms          []VM
	vmViews      []VMView
	bridgeIPList []BridgeIPList
	pxBridges    []BridgeView

	nft    *NFTManager
	dnsmas *DNSMasqManager
	px     *ProxmoxClient
}

func runTUI(cfgPath string) {
	m := &TUIMode{
		cfgPath: cfgPath,
		app:     tview.NewApplication(),
		pages:   tview.NewPages(),
		header:  tview.NewTextView(),
		footer:  tview.NewTextView(),
		focus:   map[string]tview.Primitive{},
	}
	m.header.SetDynamicColors(true)
	m.footer.SetDynamicColors(true)

	if err := m.refresh(); err != nil {
		// Still start UI; show error in footer.
		m.footer.SetText(fmt.Sprintf("[red]refresh failed:[-] %v", err))
	}

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(m.header, 1, 0, false).
		AddItem(m.pages, 0, 1, true).
		AddItem(m.footer, 1, 0, false)

	m.initPages()
	m.setTab("Dashboard")
	m.drawHeader()
	m.drawFooter()

	m.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape:
			m.app.Stop()
			return nil
		case tcell.KeyF1:
			m.setTab("Dashboard")
			return nil
		case tcell.KeyF2:
			m.setTab("Forwards")
			return nil
		case tcell.KeyF3:
			m.setTab("DHCP")
			return nil
		case tcell.KeyF4:
			m.setTab("Bridges")
			return nil
		case tcell.KeyF5:
			m.setTab("VMs")
			return nil
		case tcell.KeyF6:
			m.setTab("Web")
			return nil
		case tcell.KeyCtrlR:
			if err := m.refresh(); err != nil {
				m.footer.SetText(fmt.Sprintf("[red]refresh failed:[-] %v", err))
			}
			m.redrawAll()
			return nil
		}
		return ev
	})

	if err := m.app.SetRoot(layout, true).Run(); err != nil {
		panic(err)
	}
}

func (m *TUIMode) initPages() {
	m.pages.AddPage("Dashboard", m.dashboardPage(), true, false)
	m.pages.AddPage("Forwards", m.forwardsPage(), true, false)
	m.pages.AddPage("DHCP", m.dhcpPage(), true, false)
	m.pages.AddPage("Bridges", m.bridgesPage(), true, false)
	m.pages.AddPage("VMs", m.vmsPage(), true, false)
	m.pages.AddPage("Web", m.webPage(), true, false)
}

func (m *TUIMode) setTab(name string) {
	m.tabName = name
	m.pages.SwitchToPage(name)
	if p := m.focus[name]; p != nil {
		m.app.SetFocus(p)
	}
	m.drawHeader()
	m.drawFooter()
}

func (m *TUIMode) drawHeader() {
	// Function keys for predictable navigation in a tty.
	m.header.SetText(fmt.Sprintf(
		"[::b]PNAT TUI[::-]  F1 Dashboard | F2 Forwards | F3 DHCP | F4 Bridges | F5 VMs | F6 Web   (Ctrl+R refresh, Esc quit)   [gray]%s[-]",
		m.tabName,
	))
}

func (m *TUIMode) drawFooter() {
	webState := "unknown"
	if out, err := exec.Command("systemctl", "is-active", "--quiet", "pnat").CombinedOutput(); err == nil {
		_ = out
		webState = "active"
	} else {
		webState = "inactive"
	}
	addr := ""
	if m.cfg != nil {
		addr = m.cfg.ListenAddr
	}
	m.footer.SetText(fmt.Sprintf("[gray]web:[-] %s  [gray]listen:[-] %s", webState, addr))
}

func (m *TUIMode) redrawAll() {
	// Recreate all pages (simple, safe) to reflect updated data.
	m.pages = tview.NewPages()
	m.focus = map[string]tview.Primitive{}
	m.initPages()
	m.setTab(m.tabName)
}

func (m *TUIMode) refresh() error {
	cfg, err := LoadConfig(m.cfgPath)
	if err != nil {
		return err
	}
	m.cfg = cfg
	m.nft = NewNFTManager()
	m.dnsmas = NewDNSMasqManager()
	m.px = NewProxmoxClient(cfg.ProxmoxURL, cfg.ProxmoxTokenID, cfg.ProxmoxSecret, cfg.ProxmoxNode)

	leases, _ := m.dnsmas.Leases()
	m.leases = leases
	vms, _ := m.px.ListVMs()
	m.vms = vms
	m.vmViews = buildVMViews(m.px, vms, leases)
	m.bridgeIPList = buildBridgeIPLists(m.cfg, m.vmViews)
	m.pxBridges = buildBridgeViews(m.px, m.cfg)
	return nil
}

func (m *TUIMode) apply() error {
	m.cfg.Lock()
	defer m.cfg.Unlock()
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if err := m.nft.Apply(m.cfg); err != nil {
		return err
	}
	if err := m.dnsmas.Apply(m.cfg); err != nil {
		return err
	}
	return nil
}

func (m *TUIMode) dashboardPage() tview.Primitive {
	box := tview.NewFlex().SetDirection(tview.FlexRow)

	bridges := tview.NewTable().SetBorders(false)
	bridges.SetTitle("Bridges (t=toggle NAT, d=edit DHCP)").SetBorder(true)
	bridges.SetFixed(1, 0)
	bridges.SetSelectable(true, false)
	bridges.Select(1, 0)
	m.focus["Dashboard"] = bridges

	setCell := func(r, c int, txt string, color tcell.Color) {
		bridges.SetCell(r, c, tview.NewTableCell(txt).SetTextColor(color))
	}

	setCell(0, 0, "Bridge", tcell.ColorYellow)
	setCell(0, 1, "Subnet", tcell.ColorYellow)
	setCell(0, 2, "Gateway", tcell.ColorYellow)
	setCell(0, 3, "NAT", tcell.ColorYellow)
	setCell(0, 4, "DHCP", tcell.ColorYellow)
	setCell(0, 5, "Forwards", tcell.ColorYellow)

	for i, b := range m.cfg.Bridges {
		r := i + 1
		setCell(r, 0, b.Name, tcell.ColorWhite)
		setCell(r, 1, b.Subnet, tcell.ColorWhite)
		setCell(r, 2, b.GatewayIP, tcell.ColorWhite)
		if b.NATEnabled {
			setCell(r, 3, "ON", tcell.ColorGreen)
		} else {
			setCell(r, 3, "OFF", tcell.ColorGray)
		}
		if b.DHCP != nil {
			setCell(r, 4, fmt.Sprintf("%s-%s", b.DHCP.RangeStart, b.DHCP.RangeEnd), tcell.ColorGreen)
		} else {
			setCell(r, 4, "disabled", tcell.ColorGray)
		}
		setCell(r, 5, strconv.Itoa(len(b.Forwards)), tcell.ColorWhite)
	}

	bridges.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		row, _ := bridges.GetSelection()
		if row <= 0 || row-1 >= len(m.cfg.Bridges) {
			return ev
		}
		switch ev.Rune() {
		case 't':
			name := m.cfg.Bridges[row-1].Name
			m.cfg.Lock()
			br := m.cfg.FindBridge(name)
			if br != nil {
				br.NATEnabled = !br.NATEnabled
			}
			m.cfg.Unlock()
			if err := m.apply(); err != nil {
				m.footer.SetText(fmt.Sprintf("[red]apply failed:[-] %v", err))
			} else {
				_ = m.refresh()
				m.redrawAll()
			}
			return nil
		case 'd':
			m.setTab("DHCP")
			return nil
		}
		return ev
	})

	status := tview.NewTextView().SetDynamicColors(true)
	status.SetBorder(true).SetTitle("Status")
	status.SetText(fmt.Sprintf(
		"Leases: %d\nVMs: %d\n\nTip: use F-keys to switch tabs.\n",
		len(m.leases), len(m.vms),
	))

	box.AddItem(bridges, 0, 3, true)
	box.AddItem(status, 0, 1, false)
	return box
}

func (m *TUIMode) forwardsPage() tview.Primitive {
	root := tview.NewFlex().SetDirection(tview.FlexRow)

	table := tview.NewTable().SetBorders(false)
	table.SetTitle("Port Forwards (a=add, t=toggle, x=delete)").SetBorder(true)
	table.SetFixed(1, 0)
	table.SetSelectable(true, false)
	table.Select(1, 0)
	m.focus["Forwards"] = table

	h := []string{"Bridge", "Proto", "Ext", "Int", "Comment", "Enabled"}
	for i, s := range h {
		table.SetCell(0, i, tview.NewTableCell(s).SetTextColor(tcell.ColorYellow))
	}

	type rowRef struct {
		bridge string
		id     string
	}
	var refs []rowRef
	r := 1
	for _, b := range m.cfg.Bridges {
		for _, f := range b.Forwards {
			table.SetCell(r, 0, tview.NewTableCell(b.Name))
			table.SetCell(r, 1, tview.NewTableCell(f.Protocol))
			table.SetCell(r, 2, tview.NewTableCell(strconv.Itoa(int(f.ExtPort))))
			table.SetCell(r, 3, tview.NewTableCell(fmt.Sprintf("%s:%d", f.IntIP, f.IntPort)))
			table.SetCell(r, 4, tview.NewTableCell(f.Comment))
			if f.Enabled {
				table.SetCell(r, 5, tview.NewTableCell("ON").SetTextColor(tcell.ColorGreen))
			} else {
				table.SetCell(r, 5, tview.NewTableCell("OFF").SetTextColor(tcell.ColorGray))
			}
			refs = append(refs, rowRef{bridge: b.Name, id: f.ID})
			r++
		}
	}

	addForm := func() {
		form := tview.NewForm()
		form.SetBorder(true).SetTitle("Add Forward").SetTitleAlign(tview.AlignLeft)

		bridgeNames := make([]string, 0, len(m.cfg.Bridges))
		for _, b := range m.cfg.Bridges {
			bridgeNames = append(bridgeNames, b.Name)
		}
		sort.Strings(bridgeNames)
		if len(bridgeNames) == 0 {
			m.footer.SetText("[red]no bridges configured[-]")
			return
		}

		protos := []string{"tcp", "udp", "tcp+udp"}
		popPorts := []int{22, 80, 443, 53, 3389, 5900, 8006, 8080, 8443, 3306, 5432, 6379}

		var selBridge = bridgeNames[0]
		var proto = "tcp"
		var extPort = "22"
		var intIP = ""
		var intPort = "22"
		var comment = ""

		form.AddDropDown("Bridge", bridgeNames, 0, func(option string, _ int) {
			selBridge = option
			// best-effort: set internal IP to first suggested IP from that bridge
			for _, l := range m.bridgeIPList {
				if l.Bridge != selBridge {
					continue
				}
				if len(l.Options) > 0 {
					intIP = l.Options[0].IP
				}
			}
		})
		form.AddDropDown("Protocol", protos, 0, func(option string, _ int) { proto = option })
		form.AddInputField("External Port", extPort, 6, func(textToCheck string, lastChar rune) bool {
			if textToCheck == "" {
				return true
			}
			n, err := strconv.Atoi(textToCheck)
			return err == nil && n >= 1 && n <= 65535
		}, func(text string) { extPort = text })

		// Quick-select popular external ports (just a dropdown that fills the field).
		portOpts := make([]string, 0, len(popPorts))
		for _, p := range popPorts {
			portOpts = append(portOpts, strconv.Itoa(p))
		}
		form.AddDropDown("Popular Ext", portOpts, 0, func(option string, _ int) { extPort = option })

		form.AddInputField("Internal IP", intIP, 15, func(textToCheck string, lastChar rune) bool {
			if textToCheck == "" {
				return true
			}
			return net.ParseIP(textToCheck) != nil
		}, func(text string) { intIP = text })

		// Dropdown of VM IPs for selected bridge.
		var vmIPOpts []string
		for _, l := range m.bridgeIPList {
			if l.Bridge == selBridge {
				for _, o := range l.Options {
					vmIPOpts = append(vmIPOpts, fmt.Sprintf("%s  %s", o.IP, o.Label))
				}
			}
		}
		form.AddDropDown("VM IPs", vmIPOpts, 0, func(option string, _ int) {
			fields := strings.Fields(option)
			if len(fields) > 0 {
				intIP = fields[0]
			}
		})

		form.AddInputField("Internal Port", intPort, 6, func(textToCheck string, lastChar rune) bool {
			if textToCheck == "" {
				return true
			}
			n, err := strconv.Atoi(textToCheck)
			return err == nil && n >= 1 && n <= 65535
		}, func(text string) { intPort = text })
		form.AddDropDown("Popular Int", portOpts, 0, func(option string, _ int) { intPort = option })
		form.AddInputField("Comment", comment, 40, nil, func(text string) { comment = text })

		form.AddButton("Add", func() {
			ep, _ := strconv.Atoi(extPort)
			ip, _ := strconv.Atoi(intPort)
			m.cfg.Lock()
			br := m.cfg.FindBridge(selBridge)
			if br != nil {
				br.Forwards = append(br.Forwards, PortForward{
					ID:       generateID(),
					Protocol: proto,
					ExtPort:  uint16(ep),
					IntIP:    intIP,
					IntPort:  uint16(ip),
					Comment:  comment,
					Enabled:  true,
				})
			}
			m.cfg.Unlock()

			if err := m.apply(); err != nil {
				m.footer.SetText(fmt.Sprintf("[red]apply failed:[-] %v", err))
				return
			}
			_ = m.refresh()
			m.redrawAll()
			m.pages.HidePage("modal")
		})
		form.AddButton("Cancel", func() { m.pages.HidePage("modal") })
		form.SetCancelFunc(func() { m.pages.HidePage("modal") })

		m.pages.AddAndSwitchToPage("modal", modal(form, 100, 28), true)
		m.app.SetFocus(form)
	}

	table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'a':
			addForm()
			return nil
		case 't':
			row, _ := table.GetSelection()
			if row <= 0 || row-1 >= len(refs) {
				return ev
			}
			ref := refs[row-1]
			m.cfg.Lock()
			_, f := m.cfg.FindForward(ref.id)
			if f != nil {
				f.Enabled = !f.Enabled
			}
			m.cfg.Unlock()
			if err := m.apply(); err != nil {
				m.footer.SetText(fmt.Sprintf("[red]apply failed:[-] %v", err))
			} else {
				_ = m.refresh()
				m.redrawAll()
			}
			return nil
		case 'x':
			row, _ := table.GetSelection()
			if row <= 0 || row-1 >= len(refs) {
				return ev
			}
			ref := refs[row-1]
			m.cfg.Lock()
			_ = m.cfg.DeleteForward(ref.id)
			m.cfg.Unlock()
			if err := m.apply(); err != nil {
				m.footer.SetText(fmt.Sprintf("[red]apply failed:[-] %v", err))
			} else {
				_ = m.refresh()
				m.redrawAll()
			}
			return nil
		}
		return ev
	})

	root.AddItem(table, 0, 1, true)
	return root
}

func (m *TUIMode) dhcpPage() tview.Primitive {
	root := tview.NewFlex().SetDirection(tview.FlexRow)

	table := tview.NewTable().SetBorders(false)
	table.SetTitle("DHCP (e=edit for selected bridge)").SetBorder(true)
	table.SetFixed(1, 0)
	table.SetSelectable(true, false)
	table.Select(1, 0)
	m.focus["DHCP"] = table
	table.SetCell(0, 0, tview.NewTableCell("Bridge").SetTextColor(tcell.ColorYellow))
	table.SetCell(0, 1, tview.NewTableCell("Subnet").SetTextColor(tcell.ColorYellow))
	table.SetCell(0, 2, tview.NewTableCell("Range").SetTextColor(tcell.ColorYellow))
	table.SetCell(0, 3, tview.NewTableCell("DNS").SetTextColor(tcell.ColorYellow))

	for i, b := range m.cfg.Bridges {
		r := i + 1
		table.SetCell(r, 0, tview.NewTableCell(b.Name))
		table.SetCell(r, 1, tview.NewTableCell(b.Subnet))
		if b.DHCP != nil {
			table.SetCell(r, 2, tview.NewTableCell(fmt.Sprintf("%s-%s", b.DHCP.RangeStart, b.DHCP.RangeEnd)).SetTextColor(tcell.ColorGreen))
			dns := b.DHCP.DNS1
			if b.DHCP.DNS2 != "" {
				dns += "," + b.DHCP.DNS2
			}
			table.SetCell(r, 3, tview.NewTableCell(dns))
		} else {
			table.SetCell(r, 2, tview.NewTableCell("disabled").SetTextColor(tcell.ColorGray))
			table.SetCell(r, 3, tview.NewTableCell("-"))
		}
	}

	leases := tview.NewTable().SetBorders(false)
	leases.SetTitle(fmt.Sprintf("Leases (%d)", len(m.leases))).SetBorder(true)
	leases.SetFixed(1, 0)
	leases.SetSelectable(true, false)
	leases.Select(1, 0)
	leases.SetCell(0, 0, tview.NewTableCell("IP").SetTextColor(tcell.ColorYellow))
	leases.SetCell(0, 1, tview.NewTableCell("MAC").SetTextColor(tcell.ColorYellow))
	leases.SetCell(0, 2, tview.NewTableCell("Host").SetTextColor(tcell.ColorYellow))
	for i, l := range m.leases {
		r := i + 1
		leases.SetCell(r, 0, tview.NewTableCell(l.IP))
		leases.SetCell(r, 1, tview.NewTableCell(l.MAC))
		leases.SetCell(r, 2, tview.NewTableCell(l.Hostname))
	}

	editForm := func(b *BridgeConfig) {
		form := tview.NewForm()
		form.SetBorder(true).SetTitle("DHCP settings").SetTitleAlign(tview.AlignLeft)

		enabled := b.DHCP != nil
		rangeStart, rangeEnd, leaseTime, dns1, dns2 := "", "", "12h", "1.1.1.1", "8.8.8.8"
		if b.DHCP != nil {
			rangeStart, rangeEnd = b.DHCP.RangeStart, b.DHCP.RangeEnd
			if b.DHCP.LeaseTime != "" {
				leaseTime = b.DHCP.LeaseTime
			}
			dns1, dns2 = b.DHCP.DNS1, b.DHCP.DNS2
		}

		form.AddCheckbox("Enable DHCP", enabled, func(checked bool) { enabled = checked })
		form.AddInputField("Range start", rangeStart, 15, nil, func(text string) { rangeStart = text })
		form.AddInputField("Range end", rangeEnd, 15, nil, func(text string) { rangeEnd = text })
		form.AddInputField("Lease time", leaseTime, 10, nil, func(text string) { leaseTime = text })
		form.AddInputField("DNS1", dns1, 15, nil, func(text string) { dns1 = text })
		form.AddInputField("DNS2", dns2, 15, nil, func(text string) { dns2 = text })

		form.AddButton("Save", func() {
			m.cfg.Lock()
			br := m.cfg.FindBridge(b.Name)
			if br != nil {
				if !enabled {
					br.DHCP = nil
				} else {
					br.DHCP = &DHCPConfig{
						RangeStart: rangeStart,
						RangeEnd:   rangeEnd,
						LeaseTime:  leaseTime,
						DNS1:       dns1,
						DNS2:       dns2,
					}
				}
			}
			m.cfg.Unlock()
			if err := m.apply(); err != nil {
				m.footer.SetText(fmt.Sprintf("[red]apply failed:[-] %v", err))
				return
			}
			_ = m.refresh()
			m.redrawAll()
			m.pages.HidePage("modal")
		})
		form.AddButton("Cancel", func() { m.pages.HidePage("modal") })
		form.SetCancelFunc(func() { m.pages.HidePage("modal") })

		m.pages.AddAndSwitchToPage("modal", modal(form, 80, 20), true)
		m.app.SetFocus(form)
	}

	table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Rune() != 'e' {
			return ev
		}
		row, _ := table.GetSelection()
		if row <= 0 || row-1 >= len(m.cfg.Bridges) {
			return nil
		}
		b := m.cfg.Bridges[row-1]
		editForm(&b)
		return nil
	})

	root.AddItem(table, 0, 2, true)
	root.AddItem(leases, 0, 1, false)
	return root
}

func (m *TUIMode) bridgesPage() tview.Primitive {
	// Placeholder with proxmox bridges list (read-only for now).
	table := tview.NewTable().SetBorders(false)
	table.SetTitle("Proxmox Bridges (read-only in TUI v1)").SetBorder(true)
	table.SetFixed(1, 0)
	table.SetSelectable(true, false)
	table.Select(1, 0)
	m.focus["Bridges"] = table

	h := []string{"Name", "CIDR", "Ports", "Managed"}
	for i, s := range h {
		table.SetCell(0, i, tview.NewTableCell(s).SetTextColor(tcell.ColorYellow))
	}
	for i, b := range m.pxBridges {
		r := i + 1
		table.SetCell(r, 0, tview.NewTableCell(b.Name))
		table.SetCell(r, 1, tview.NewTableCell(b.CIDR))
		table.SetCell(r, 2, tview.NewTableCell(b.Ports))
		if b.Managed {
			table.SetCell(r, 3, tview.NewTableCell("yes").SetTextColor(tcell.ColorGreen))
		} else {
			table.SetCell(r, 3, tview.NewTableCell("no").SetTextColor(tcell.ColorGray))
		}
	}
	return table
}

func (m *TUIMode) vmsPage() tview.Primitive {
	table := tview.NewTable().SetBorders(false)
	table.SetTitle("VMs (read-only in TUI v1)").SetBorder(true)
	table.SetFixed(1, 0)
	table.SetSelectable(true, false)
	table.Select(1, 0)
	m.focus["VMs"] = table

	h := []string{"VMID", "Name", "Type", "Status", "NICs"}
	for i, s := range h {
		table.SetCell(0, i, tview.NewTableCell(s).SetTextColor(tcell.ColorYellow))
	}
	for i, vm := range m.vmViews {
		r := i + 1
		table.SetCell(r, 0, tview.NewTableCell(strconv.Itoa(vm.VMID)))
		table.SetCell(r, 1, tview.NewTableCell(vm.Name))
		table.SetCell(r, 2, tview.NewTableCell(vm.Type))
		table.SetCell(r, 3, tview.NewTableCell(vm.Status))
		var nics []string
		for _, n := range vm.NICs {
			ip := n.LeaseIP
			if ip == "" && len(n.IPs) > 0 {
				ip = n.IPs[0]
			}
			nics = append(nics, fmt.Sprintf("%s:%s %s", n.Key, n.Bridge, ip))
		}
		table.SetCell(r, 4, tview.NewTableCell(strings.Join(nics, " | ")))
	}
	return table
}

func (m *TUIMode) webPage() tview.Primitive {
	f := tview.NewForm()
	f.SetBorder(true).SetTitle("Web Interface Control").SetTitleAlign(tview.AlignLeft)
	m.focus["Web"] = f

	curAddr := ""
	if m.cfg != nil {
		curAddr = m.cfg.ListenAddr
	}
	host, portStr, _ := net.SplitHostPort(curAddr)
	if host == "" {
		host = "0.0.0.0"
	}
	if portStr == "" {
		portStr = "9090"
	}

	port := portStr
	f.AddInputField("Port", port, 6, func(textToCheck string, lastChar rune) bool {
		if textToCheck == "" {
			return true
		}
		n, err := strconv.Atoi(textToCheck)
		return err == nil && n >= 1 && n <= 65535
	}, func(text string) { port = text })

	f.AddButton("Start Web", func() {
		_ = exec.Command("systemctl", "start", "pnat").Run()
		m.drawFooter()
	})
	f.AddButton("Stop Web", func() {
		_ = exec.Command("systemctl", "stop", "pnat").Run()
		// Keep DHCP alive if configured (pnat-dnsmasq is PartOf pnat.service).
		hasDHCP := false
		for _, b := range m.cfg.Bridges {
			if b.DHCP != nil {
				hasDHCP = true
				break
			}
		}
		if hasDHCP {
			_ = exec.Command("systemctl", "start", "pnat-dnsmasq.service").Run()
		}
		m.drawFooter()
	})
	f.AddButton("Set Port (restart web)", func() {
		if port == "" {
			return
		}
		m.cfg.Lock()
		m.cfg.ListenAddr = net.JoinHostPort(host, port)
		m.cfg.Unlock()
		if err := m.cfg.Save(); err != nil {
			m.footer.SetText(fmt.Sprintf("[red]save failed:[-] %v", err))
			return
		}
		_ = exec.Command("systemctl", "restart", "pnat").Run()
		_ = m.refresh()
		m.drawFooter()
	})

	f.AddButton("Back", func() { m.setTab("Dashboard") })
	return f
}

func modal(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 1, true).
			AddItem(nil, 0, 1, false),
			width, 1, true).
		AddItem(nil, 0, 1, false)
}
