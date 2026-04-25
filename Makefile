BINARY     := praetor-server
MODULE     := github.com/ondrejsindelka/praetor-server
CMD        := ./cmd/praetor-server
BIN_DIR    := bin

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -s -w -X main.version=$(VERSION)
GOFLAGS    := CGO_ENABLED=0

.PHONY: build test lint run-dev clean

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
