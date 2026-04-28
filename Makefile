.PHONY: build build-fast build-desktop test test-go test-front test-e2e lint fmt tidy clean run run-browser

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

test: test-go test-front test-e2e

test-go:
	go test -race -timeout 120s ./...

test-front:
	cd frontend && npm test --silent || true

test-e2e: build
	cd tests/playwright && npm install --silent && npx playwright install --with-deps chromium && npx playwright test

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
