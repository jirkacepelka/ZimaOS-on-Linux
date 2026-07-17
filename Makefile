APP     := zima-connect
APPID   := io.github.jirkacepelka.ZimaConnect
PREFIX  ?= /usr/local
MANIFEST := packaging/flatpak/$(APPID).yml

.PHONY: build run test fmt vet clean install flatpak flatpak-run

build: ## Build the binary into ./dist
	@mkdir -p dist
	go build -trimpath -ldflags="-s -w" -o dist/$(APP) ./cmd/$(APP)

run: ## Build and run in the foreground (opens the browser)
	go run ./cmd/$(APP)

test: ## Run unit tests
	go test ./...

fmt: ## Format the code
	gofmt -w cmd internal

vet: ## Static checks
	go vet ./...

clean:
	rm -rf dist build-dir .flatpak-builder

install: build ## Install binary + desktop entry to $(PREFIX)
	install -Dm755 dist/$(APP) $(DESTDIR)$(PREFIX)/bin/$(APP)
	install -Dm644 packaging/flatpak/$(APPID).desktop \
		$(DESTDIR)$(PREFIX)/share/applications/$(APPID).desktop
	install -Dm644 packaging/flatpak/icons/$(APPID).svg \
		$(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/$(APPID).svg

flatpak: ## Build and install the Flatpak locally
	flatpak-builder --user --install --force-clean build-dir $(MANIFEST)

flatpak-run: ## Run the locally installed Flatpak
	flatpak run $(APPID)
