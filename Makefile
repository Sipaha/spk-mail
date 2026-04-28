.PHONY: build build-fast build-desktop release test test-go test-front test-e2e lint fmt tidy clean run run-browser

BIN_DIR := build/bin
BIN     := $(BIN_DIR)/spk-mail

build: build-frontend build-go

build-fast: build-go

build-frontend:
	cd frontend && npm ci --silent && npm run build

build-go:
	mkdir -p $(BIN_DIR)
	rm -rf cmd/spk-mail/dist
	cp -r frontend/dist cmd/spk-mail/dist || mkdir -p cmd/spk-mail/dist
	touch cmd/spk-mail/dist/.gitkeep
	CGO_ENABLED=1 go build -trimpath -ldflags="-w -s" -o $(BIN) ./cmd/spk-mail
	rm -rf cmd/spk-mail/dist
	mkdir -p cmd/spk-mail/dist
	touch cmd/spk-mail/dist/.gitkeep

build-desktop: build-frontend
	mkdir -p $(BIN_DIR)
	rm -rf cmd/spk-mail/dist && cp -r frontend/dist cmd/spk-mail/dist
	CGO_ENABLED=1 go build -tags "wails desktop_only" -trimpath -ldflags="-w -s" -o $(BIN_DIR)/spk-mail-desktop ./cmd/spk-mail
	rm -rf cmd/spk-mail/dist
	mkdir -p cmd/spk-mail/dist
	touch cmd/spk-mail/dist/.gitkeep

# release builds the desktop binary with the `production` tag set.
# Effects vs build-desktop:
#   * DevTools off (see internal/desktop/devtools_prod.go)
#   * pkg/application/application_debug.go is excluded, dropping the
#     go-git + go-billy + xanzy/ssh-agent + ProtonMail/go-crypto +
#     cloudflare/circl + groupcache transitive chain from the binary
#     (verify with `go mod why -m github.com/go-git/go-git/v5` after
#     building — it should report "not needed").
release: build-frontend
	mkdir -p $(BIN_DIR)
	rm -rf cmd/spk-mail/dist && cp -r frontend/dist cmd/spk-mail/dist
	CGO_ENABLED=1 go build -tags "wails desktop_only production" -trimpath -ldflags="-w -s" -o $(BIN_DIR)/spk-mail-release ./cmd/spk-mail
	rm -rf cmd/spk-mail/dist
	mkdir -p cmd/spk-mail/dist
	touch cmd/spk-mail/dist/.gitkeep

test: test-go test-front test-e2e

test-go:
	go test -race -timeout 120s ./...

test-front:
	cd frontend && npm test --silent || true

# test-e2e gates the chromium download on a marker file so re-runs skip
# the ~150MB pull. CI caches the playwright browser dir in
# .github/workflows/e2e.yml; this branch is the local-dev mirror. Bump
# the playwright pin in tests/playwright/package.json and the marker
# stays — `rm tests/playwright/node_modules/.playwright-browsers` to
# force a fresh install after a version bump.
test-e2e: build
	cd tests/playwright && npm install --silent
	@if [ ! -f tests/playwright/node_modules/.playwright-browsers ]; then \
		echo "  PLAYWRIGHT  installing chromium (one-time per node_modules)"; \
		cd tests/playwright && npx playwright install --with-deps chromium && touch node_modules/.playwright-browsers; \
	fi
	cd tests/playwright && npx playwright test

lint:
	golangci-lint run
	cd frontend && npm run lint --silent || true

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -rf build frontend/dist coverage.html

run: build-desktop
	$(BIN_DIR)/spk-mail-desktop

run-browser: build
	$(BIN) --browser --port=5174
