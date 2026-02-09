package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/crypto/bcrypt"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var version = "dev"

// App holds all application dependencies.
type App struct {
	cfg       *Config
	auth      Authenticator
	sessions  *SessionStore
	nft       *NFTManager
	dnsmasq   *DNSMasqManager
	proxmox   *ProxmoxClient
	templates map[string]*template.Template
}

func main() {
	configPath := flag.String("config", DefaultConfigPath(), "path to config file")
	flag.Parse()

	args := flag.Args()
	if len(args) > 0 && args[0] == "init" {
		runInit(*configPath)
		return
	}

	if len(args) > 0 && args[0] == "version" {
		fmt.Printf("pnat %s\n", version)
		return
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Printf("ERROR: failed to load config %s: %v", *configPath, err)
		fmt.Fprintf(os.Stderr, "Failed to load config %s: %v\n", *configPath, err)
		fmt.Fprintf(os.Stderr, "Run 'pnat init' to create a new config.\n")
		os.Exit(1)
	}

	auth, err := NewAuthenticator(cfg)
	if err != nil {
		log.Printf("ERROR: failed to init authenticator: %v", err)
		os.Exit(1)
	}

	baseTmpl, err := template.New("layout").Funcs(template.FuncMap{
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
	}).ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		log.Printf("ERROR: failed to parse templates: %v", err)
		os.Exit(1)
	}

	pages := []string{
		"dashboard.html",
		"forwards.html",
		"dhcp.html",
		"dhcp_form.html",
		"login.html",
	}
	templates := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		t, err := baseTmpl.Clone()
		if err != nil {
			log.Printf("ERROR: failed to clone templates: %v", err)
			os.Exit(1)
		}
		if _, err := t.ParseFS(templateFS, "templates/"+page); err != nil {
			log.Printf("ERROR: failed to parse template %s: %v", page, err)
			os.Exit(1)
		}
		templates[page] = t
	}

	sessions := NewSessionStore(cfg.SessionSecret)
	nft := NewNFTManager()
	dnsmasq := NewDNSMasqManager()
	proxmox := NewProxmoxClient(cfg.ProxmoxURL, cfg.ProxmoxTokenID, cfg.ProxmoxSecret, cfg.ProxmoxNode)

	app := &App{
		cfg:       cfg,
		auth:      auth,
		sessions:  sessions,
		nft:       nft,
		dnsmasq:   dnsmasq,
		proxmox:   proxmox,
		templates: templates,
	}

	// Apply saved state on startup
	if err := nft.Apply(cfg); err != nil {
		log.Printf("WARN: failed to apply nftables rules on startup: %v", err)
	} else {
		log.Println("nftables rules applied")
	}
	if err := dnsmasq.Apply(cfg); err != nil {
		log.Printf("WARN: failed to apply dnsmasq config on startup: %v", err)
	} else {
		log.Println("dnsmasq config applied")
	}

	mux := http.NewServeMux()
	app.SetupRoutes(mux)

	// Graceful shutdown: clean up nftables on stop
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("received signal %v, shutting down", sig)
		os.Exit(0)
	}()

	log.Printf("starting PNAT %s on %s", version, cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Printf("ERROR: server error: %v", err)
		os.Exit(1)
	}
}

func runInit(configPath string) {
	reader := bufio.NewReader(os.Stdin)
	prompt := func(label, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", label, def)
		} else {
			fmt.Printf("%s: ", label)
		}
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	fmt.Println("=== PNAT Init ===")
	fmt.Println()

	authMode := strings.ToLower(prompt("Auth mode (pam/local)", "pam"))
	var adminUser string
	var passHash string
	var pamService string
	var allowUsers []string
	switch authMode {
	case "pam":
		pamService = prompt("PAM service", "pnat")
		allowCSV := prompt("Allow users (comma-separated, empty=any)", "root")
		if strings.TrimSpace(allowCSV) != "" {
			for _, p := range strings.Split(allowCSV, ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				allowUsers = append(allowUsers, p)
			}
		}
	case "local":
		adminUser = prompt("Admin username", "admin")
		adminPass := prompt("Admin password", "")
		if adminPass == "" {
			fmt.Fprintln(os.Stderr, "Password cannot be empty")
			os.Exit(1)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bcrypt error: %v\n", err)
			os.Exit(1)
		}
		passHash = string(hash)
	default:
		fmt.Fprintln(os.Stderr, "Invalid auth mode (use pam or local)")
		os.Exit(1)
	}

	secret, err := GenerateSessionSecret()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate secret error: %v\n", err)
		os.Exit(1)
	}

	pveURL := prompt("Proxmox URL", "https://127.0.0.1:8006")
	pveTokenID := prompt("Proxmox API Token ID (e.g. root@pam!pnat)", "")
	pveSecret := prompt("Proxmox API Token Secret (UUID)", "")
	pveNode := prompt("Proxmox Node name", "pve")
	wanIface := prompt("WAN interface", "vmbr0")
	listenAddr := prompt("Listen address", "127.0.0.1:9090")

	cfg := &Config{
		ListenAddr:     listenAddr,
		AuthMode:       authMode,
		AuthPamService: pamService,
		AuthAllowUsers: allowUsers,
		AdminUser:      adminUser,
		AdminPassHash:  passHash,
		SessionSecret:  secret,
		ProxmoxURL:     pveURL,
		ProxmoxTokenID: pveTokenID,
		ProxmoxSecret:  pveSecret,
		ProxmoxNode:    pveNode,
		WanInterface:   wanIface,
		Bridges:        []BridgeConfig{},
		path:           configPath,
	}

	// Create config directory if needed
	dir := strings.TrimSuffix(configPath, "/pnat.json")
	if dir != configPath {
		os.MkdirAll(dir, 0700)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal error: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "write error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nConfig written to %s\n", configPath)
	fmt.Println("You can now start pnat with: systemctl start pnat")
}
