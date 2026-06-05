BIN     := muxray
PKG     := github.com/dandriscoll/muxray/internal/version
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT)

.PHONY: build test test-short lint fmt fmt-check vet install fixtures release clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/muxray

test:
	go test -count=1 ./...

test-short:
	go test -short -count=1 ./...

lint: fmt-check vet

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed in:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/muxray

# Regenerate golden files intentionally (provider classifications + command goldens).
fixtures:
	go test ./internal/provider ./internal/cli -run 'TestFixtures|TestTelemetryShowGolden' -update

release:
	./scripts/build-release.sh $(VERSION)

clean:
	rm -rf dist $(BIN)
