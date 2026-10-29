GO ?= go
VERSION ?= 0.1.0
MINGW_CC ?= x86_64-w64-mingw32-gcc
MINGW_WINDRES ?= x86_64-w64-mingw32-windres
BUILD_FLAGS = -buildvcs=false -trimpath
LD_FLAGS = -s -w -X main.version=$(VERSION)

.PHONY: all server server-arm64 server-all client client-linux client-windows icons windows-resource test vet release package

all: server client

server:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -ldflags '$(LD_FLAGS)' -o bin/serverupdaterd-linux-amd64 ./cmd/serverupdaterd

server-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(BUILD_FLAGS) -ldflags '$(LD_FLAGS)' -o bin/serverupdaterd-linux-arm64 ./cmd/serverupdaterd

server-all: server server-arm64

client:
	mkdir -p bin
	CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags '$(LD_FLAGS)' -o bin/ ./cmd/serverupdater

client-linux:
	mkdir -p bin
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GO) build $(BUILD_FLAGS) -ldflags '$(LD_FLAGS)' -o bin/serverupdater-linux-amd64 ./cmd/serverupdater

icons:
	$(GO) run -buildvcs=false ./tools/icon-gen

windows-resource: cmd/serverupdater/resource_windows_amd64.syso

cmd/serverupdater/resource_windows_amd64.syso: cmd/serverupdater/icon.rc internal/branding/server.ico
	cd cmd/serverupdater && $(MINGW_WINDRES) --input-format=rc --output-format=coff --target=pe-x86-64 -i icon.rc -o resource_windows_amd64.syso

client-windows: windows-resource
	mkdir -p bin
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=$(MINGW_CC) CGO_LDFLAGS=-static $(GO) build $(BUILD_FLAGS) -ldflags '$(LD_FLAGS) -H windowsgui' -o bin/serverupdater-windows-amd64.exe ./cmd/serverupdater

test:
	$(GO) test -tags ci ./...

vet:
	$(GO) vet -tags ci ./...

release: server-all client-linux client-windows
	$(MAKE) package

package:
	test -f bin/serverupdaterd-linux-amd64
	test -f bin/serverupdaterd-linux-arm64
	test -f bin/serverupdater-linux-amd64
	test -f bin/serverupdater-windows-amd64.exe
	mkdir -p dist
	tar -czf dist/serverupdater-server-linux-amd64.tar.gz README.md THIRD_PARTY_NOTICES.txt deploy/serverupdater.service scripts/install-server.sh bin/serverupdaterd-linux-amd64
	tar -czf dist/serverupdater-server-linux-arm64.tar.gz README.md THIRD_PARTY_NOTICES.txt deploy/serverupdater.service scripts/install-server.sh bin/serverupdaterd-linux-arm64
	cp bin/serverupdater-linux-amd64 bin/serverupdater-windows-amd64.exe dist/
	cp THIRD_PARTY_NOTICES.txt dist/
	tar -czf dist/serverupdater-client-linux-amd64.tar.gz README.md THIRD_PARTY_NOTICES.txt bin/serverupdater-linux-amd64
	zip -j -q dist/serverupdater-client-windows-amd64.zip bin/serverupdater-windows-amd64.exe README.md THIRD_PARTY_NOTICES.txt
	cd dist && sha256sum serverupdater-* THIRD_PARTY_NOTICES.txt > SHA256SUMS
