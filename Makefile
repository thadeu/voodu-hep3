BIN      := bin/voodu-hep3
PKG      := ./cmd/voodu-hep3
DIST     := dist
VERSION  := $(shell grep '^version:' plugin.yml | awk '{print $$2}')

GO       := go
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build test lint vet cross clean install-local

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

# cross produces the two release binaries we ship.
cross: $(DIST)/voodu-hep3_linux_amd64 $(DIST)/voodu-hep3_linux_arm64

$(DIST)/voodu-hep3_linux_amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags '$(LDFLAGS)' -o $@ $(PKG)

$(DIST)/voodu-hep3_linux_arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags '$(LDFLAGS)' -o $@ $(PKG)

install-local: build
	@if [ -z "$(PLUGINS_ROOT)" ]; then \
		echo "PLUGINS_ROOT is required (e.g. /opt/voodu/plugins)"; exit 1; \
	fi
	@mkdir -p $(PLUGINS_ROOT)/hep3/bin
	cp $(BIN) $(PLUGINS_ROOT)/hep3/bin/voodu-hep3
	cp plugin.yml $(PLUGINS_ROOT)/hep3/
	cp install uninstall $(PLUGINS_ROOT)/hep3/ 2>/dev/null || true
	@echo "installed into $(PLUGINS_ROOT)/hep3"

clean:
	rm -rf $(BIN) $(DIST)
