BINARY = pnat
VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
HOST ?= root@proxmox

.PHONY: build clean deploy

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) .

deploy: build
	scp $(BINARY) $(HOST):/usr/local/bin/pnat
	scp deploy/pnat.service deploy/pnat-dnsmasq.service $(HOST):/etc/systemd/system/
	ssh $(HOST) "chmod +x /usr/local/bin/pnat && mkdir -p /etc/pnat /var/lib/pnat && systemctl daemon-reload"

clean:
	rm -f $(BINARY)
