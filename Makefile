BINARY := bin/fundus
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/fundus-app/fundus/internal/api.Version=$(VERSION)

.PHONY: build install test run lint tidy clean appimage ui ui-linux app-test app-e2e

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/fundus
	ln -sf $(BINARY) fundus

install: build
	install -Dm755 $(BINARY) $(HOME)/.local/bin/fundus

test:
	go test ./...

run: build
	$(BINARY) serve

tidy:
	go mod tidy

lint:
	go vet ./...

clean:
	rm -rf bin dist

# ---------------------------------------------------------------------------
# Flutter client (app/). Requires the Flutter SDK on PATH.

FLUTTER ?= flutter
WEBUI := internal/webui/web

# Build the web client and embed it into the Go binary (run `make build` after).
ui:
	cd app && $(FLUTTER) build web --release --base-href /
	find $(WEBUI) -mindepth 1 ! -name .gitkeep -delete
	cp -r app/build/web/. $(WEBUI)/

# Build the Linux desktop app (app/build/linux/x64/release/bundle/fundus_app).
ui-linux:
	cd app && $(FLUTTER) build linux --release

app-test:
	cd app && $(FLUTTER) analyze && $(FLUTTER) test

# Linux desktop app + daemon as one AppImage (needs make build and make ui-linux first).
appimage:
	scripts/appimage.sh $(VERSION)

# End-to-end: the real desktop app against a real daemon (needs a display;
# use `xvfb-run -a make app-e2e` or a headless Wayland compositor).
FUNDUS_BIN ?= $(abspath $(BINARY))
app-e2e: $(BINARY)
	cd app && FUNDUS_BIN=$(FUNDUS_BIN) $(FLUTTER) test integration_test -d linux

$(BINARY):
	$(MAKE) build
