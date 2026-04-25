BINARY     := praetor-server
MODULE     := github.com/ondrejsindelka/praetor-server
CMD        := ./cmd/praetor-server
BIN_DIR    := bin

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -s -w -X main.version=$(VERSION)
GOFLAGS    := CGO_ENABLED=0

.PHONY: build test lint run-dev clean token-issue enroll-smoke-test

build:
	$(GOFLAGS) go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

test:
	go test -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run ./...

run-dev:
	go run $(CMD) --config examples/server.yaml

clean:
	rm -rf $(BIN_DIR) tmp coverage.out

# --- Docker Compose ---
.PHONY: compose-up compose-down compose-ps compose-logs compose-reset db-shell

compose-up:
	docker compose up -d

compose-down:
	docker compose down

compose-ps:
	docker compose ps

compose-logs:
	docker compose logs -f

compose-reset:
	docker compose down -v

db-shell:
	docker compose exec postgres psql -U praetor

# --- Token management ---
token-issue: build
	$(BIN_DIR)/$(BINARY) token issue --label "$(LABEL)" --config examples/server.yaml --ttl 15m

enroll-smoke-test: build
	@echo "Starting enrollment smoke test..."
	@mkdir -p tmp/data
	$(BIN_DIR)/$(BINARY) migrate up --config examples/server.yaml
	$(BIN_DIR)/$(BINARY) token issue --label "smoke-test" --config examples/server.yaml --ttl 15m
	@echo "Token issued. Run integration tests for full enrollment flow:"
	@echo "  TEST_POSTGRES_DSN=... go test -v -tags integration ./internal/enrollment/..."

# --- Migrations ---
.PHONY: migrate-up migrate-down migrate-status

migrate-up: build
	$(BIN_DIR)/$(BINARY) migrate up --config examples/server.yaml

migrate-down: build
	$(BIN_DIR)/$(BINARY) migrate down --config examples/server.yaml

migrate-status: build
	$(BIN_DIR)/$(BINARY) migrate status --config examples/server.yaml
