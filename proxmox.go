package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ProxmoxClient is a read-only client for the Proxmox VE API.
type ProxmoxClient struct {
	baseURL string
	tokenID string
	secret  string
	node    string
	client  *http.Client
}

func NewProxmoxClient(baseURL, tokenID, secret, node string) *ProxmoxClient {
	return &ProxmoxClient{
		baseURL: baseURL,
		tokenID: tokenID,
		secret:  secret,
		node:    node,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

func (p *ProxmoxClient) doGet(path string) ([]byte, error) {
	return p.doRequest("GET", path, nil)
}

func (p *ProxmoxClient) doRequest(method, path string, values url.Values) ([]byte, error) {
	url := fmt.Sprintf("%s/api2/json%s", p.baseURL, path)
	var reqBody io.Reader
	if values != nil {
		reqBody = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", p.tokenID, p.secret))
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxmox request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("proxmox API %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (p *ProxmoxClient) doPut(path string, values url.Values) ([]byte, error) {
	return p.doRequest("PUT", path, values)
}

// ListVMs returns all QEMU VMs and LXC containers on the node.
func (p *ProxmoxClient) ListVMs() ([]VM, error) {
	if p.baseURL == "" || p.tokenID == "" {
		return nil, nil
	}

	var vms []VM

	// QEMU VMs
	qemuData, err := p.doGet(fmt.Sprintf("/nodes/%s/qemu", p.node))
	if err != nil {
		log.Printf("WARN: failed to list QEMU VMs: %v", err)
	} else {
		var resp struct {
			Data []struct {
				VMID   int    `json:"vmid"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := json.Unmarshal(qemuData, &resp); err == nil {
			for _, v := range resp.Data {
				vms = append(vms, VM{VMID: v.VMID, Name: v.Name, Status: v.Status, Type: "qemu"})
			}
		}
	}

	// LXC containers
	lxcData, err := p.doGet(fmt.Sprintf("/nodes/%s/lxc", p.node))
	if err != nil {
		log.Printf("WARN: failed to list LXC containers: %v", err)
	} else {
		var resp struct {
			Data []struct {
				VMID   int    `json:"vmid"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := json.Unmarshal(lxcData, &resp); err == nil {
			for _, v := range resp.Data {
				vms = append(vms, VM{VMID: v.VMID, Name: v.Name, Status: v.Status, Type: "lxc"})
			}
		}
	}

	sort.Slice(vms, func(i, j int) bool { return vms[i].VMID < vms[j].VMID })
	return vms, nil
}

// ProxmoxNetwork describes a network interface in Proxmox.
type ProxmoxNetwork struct {
	Iface       string `json:"iface"`
	Type        string `json:"type"`
	CIDR        string `json:"cidr"`
	Address     string `json:"address"`
	Netmask     string `json:"netmask"`
	Method      string `json:"method"`
	BridgePorts string `json:"bridge_ports"`
	BridgeFD    string `json:"bridge_fd"`
	BridgeSTP   string `json:"bridge_stp"`
	Autostart   int    `json:"autostart"`
}

// ListNetworks returns all network interfaces on the node.
func (p *ProxmoxClient) ListNetworks() ([]ProxmoxNetwork, error) {
	if p.baseURL == "" || p.tokenID == "" {
		return nil, nil
	}

	data, err := p.doGet(fmt.Sprintf("/nodes/%s/network", p.node))
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []ProxmoxNetwork `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse network list: %w", err)
	}
	return resp.Data, nil
}

// CreateBridge creates a Linux bridge on the node via the Proxmox API.
func (p *ProxmoxClient) CreateBridge(iface, cidr, bridgePorts string) error {
	if p.baseURL == "" || p.tokenID == "" {
		return fmt.Errorf("proxmox API not configured")
	}
	values := url.Values{}
	values.Set("iface", iface)
	values.Set("type", "bridge")
	values.Set("autostart", "1")
	if bridgePorts != "" {
		values.Set("bridge_ports", bridgePorts)
	}
	if cidr != "" {
		values.Set("cidr", cidr)
	}

	_, err := p.doRequest("POST", fmt.Sprintf("/nodes/%s/network", p.node), values)
	return err
}

// ReloadNetwork applies pending network changes via ifreload.
func (p *ProxmoxClient) ReloadNetwork() error {
	if p.baseURL == "" || p.tokenID == "" {
		return fmt.Errorf("proxmox API not configured")
	}
	_, err := p.doRequest("PUT", fmt.Sprintf("/nodes/%s/network", p.node), url.Values{})
	return err
}

func (p *ProxmoxClient) GetVMConfig(vmType string, vmid int) (map[string]string, error) {
	if p.baseURL == "" || p.tokenID == "" {
		return nil, nil
	}
	var path string
	switch vmType {
	case "qemu":
		path = fmt.Sprintf("/nodes/%s/qemu/%d/config", p.node, vmid)
	case "lxc":
		path = fmt.Sprintf("/nodes/%s/lxc/%d/config", p.node, vmid)
	default:
		return nil, fmt.Errorf("unknown VM type %q", vmType)
	}
	data, err := p.doGet(path)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse vm config: %w", err)
	}
	out := make(map[string]string, len(resp.Data))
	for k, v := range resp.Data {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = fmt.Sprintf("%.0f", t)
		case bool:
			if t {
				out[k] = "1"
			} else {
				out[k] = "0"
			}
		default:
			// ignore complex values
		}
	}
	return out, nil
}

func (p *ProxmoxClient) SetVMConfig(vmType string, vmid int, values url.Values) error {
	if p.baseURL == "" || p.tokenID == "" {
		return fmt.Errorf("proxmox API not configured")
	}
	var path string
	switch vmType {
	case "qemu":
		path = fmt.Sprintf("/nodes/%s/qemu/%d/config", p.node, vmid)
	case "lxc":
		path = fmt.Sprintf("/nodes/%s/lxc/%d/config", p.node, vmid)
	default:
		return fmt.Errorf("unknown VM type %q", vmType)
	}
	_, err := p.doPut(path, values)
	return err
}
