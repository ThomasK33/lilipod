.PHONY: all lilipod pty coverage download-busybox download-slirp4netns

# Define slirp4netns version and architecture
SLIRP_VERSION := 1.2.0
SLIRP_ARCH := x86_64
SLIRP_BINARY_NAME := slirp4netns-$(SLIRP_ARCH)
SLIRP_DOWNLOAD_URL := https://github.com/rootless-containers/slirp4netns/releases/download/v$(SLIRP_VERSION)/$(SLIRP_BINARY_NAME)
SLIRP_LOCAL_PATH := slirp4netns

all: download-busybox download-slirp4netns pty lilipod

clean:
	@rm -f lilipod
	@rm -f pty
	@rm -f pty.tar.gz
	@rm -f busybox
	@rm -f $(SLIRP_LOCAL_PATH)

lilipod: download-slirp4netns
	@rm -f lilipod
	CGO_ENABLED=0 go build -mod vendor -ldflags="-s -w -X 'github.com/89luca89/lilipod/pkg/constants.Version=$${RELEASE_VERSION:-0.0.0}'" -o lilipod main.go

coverage:
	@rm -rf coverage/*
	@mkdir -p coverage
	CGO_ENABLED=0 go build -mod vendor -cover -o coverage/pty ptyagent/main.go ptyagent/pty.go
	@rm -f pty
	@rm -f pty.tar.gz
	CGO_ENABLED=0 go build -mod vendor -gcflags=all="-l -B -C" -ldflags="-s -w" -o pty ptyagent/main.go ptyagent/pty.go
	tar czfv pty.tar.gz pty
	@wget -c "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
	CGO_ENABLED=0 go build -mod vendor -cover -o coverage/lilipod main.go

download-busybox:
	@echo "Downloading busybox..."
	@wget -c "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox" -O busybox
	@chmod +x busybox

download-slirp4netns:
	@echo "Downloading slirp4netns (v$(SLIRP_VERSION), arch=$(SLIRP_ARCH))..."
	@wget -c "$(SLIRP_DOWNLOAD_URL)" -O "$(SLIRP_LOCAL_PATH)"
	@chmod +x "$(SLIRP_LOCAL_PATH)"

pty:
	@rm -f pty
	@rm -f pty.tar.gz
	CGO_ENABLED=0 go build -mod vendor -gcflags=all="-l -B -C" -ldflags="-s -w -X 'main.version=$${RELEASE_VERSION:-0.0.0}'" -o pty ptyagent/main.go ptyagent/pty.go
	tar czfv pty.tar.gz pty

trivy:
	@trivy fs --scanners vuln .
