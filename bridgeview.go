package main

import (
	"log"
	"sort"
)

func buildBridgeViews(px *ProxmoxClient, cfg *Config) []BridgeView {
	networks, err := px.ListNetworks()
	if err != nil {
		log.Printf("WARN: failed to list networks: %v", err)
		return nil
	}

	managed := map[string]bool{}
	for _, b := range cfg.Bridges {
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

