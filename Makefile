BINARY := invest
BUILD_VERSION ?= $(if $(VERSION),$(VERSION),dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X 'investment-analyzer/internal/cli.buildVersion=$(BUILD_VERSION)' \
           -X 'investment-analyzer/internal/cli.buildCommit=$(COMMIT)' \
           -X 'investment-analyzer/internal/cli.buildDate=$(BUILD_DATE)'

.PHONY: build test lint tidy clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/invest

test:
	go test ./...

lint:
	docker run --rm -v $(CURDIR):/app -w /app golangci/golangci-lint:v2.11.4-alpine golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
