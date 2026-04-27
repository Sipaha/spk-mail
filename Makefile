.PHONY: build build-fast test test-go test-front lint fmt tidy clean run-browser

BIN_DIR := build/bin
BIN     := $(BIN_DIR)/spk-mail

build: build-frontend build-go

build-fast: build-go

build-frontend:
	cd frontend && npm ci --silent && npm run build

build-go:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 go build -trimpath -ldflags="-w -s" -o $(BIN) ./cmd/spk-mail

test: test-go test-front

test-go:
	go test -race -timeout 120s ./...

test-front:
	cd frontend && npm test --silent || true

lint:
	golangci-lint run
	cd frontend && npm run lint --silent || true

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -rf build frontend/dist coverage.html

run-browser: build
	$(BIN) --browser --port=5174
