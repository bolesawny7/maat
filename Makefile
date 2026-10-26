# maat developer Makefile. Targets assume Go 1.26+ and, for the snap, snapcraft.

GO ?= go
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BIN := bin

.PHONY: all build test race fuzz vet fmt invariants clean snap maatctl selftest-dev

all: build test vet invariants

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/maat ./cmd/maat
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/maatctl ./cmd/maatctl

test:
	$(GO) test ./...

# race runs the suite under the data-race detector (notably the trail's lock).
race:
	$(GO) test -race ./...

# fuzz runs each untrusted-input fuzz target briefly, as a smoke check. The
# corpus seeds already run as unit tests under `make test`.
fuzz:
	$(GO) test ./internal/collect/ -run '^$$' -fuzz '^FuzzParseJournalJSON$$' -fuzztime=10s
	$(GO) test ./internal/collect/ -run '^$$' -fuzz '^FuzzParseAppArmorDenial$$' -fuzztime=10s
	$(GO) test ./internal/condense/ -run '^$$' -fuzz '^FuzzRedact$$' -fuzztime=10s
	$(GO) test ./internal/condense/ -run '^$$' -fuzz '^FuzzDrainAdd$$' -fuzztime=10s

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal bench/harness

# invariants runs the security-invariant tests described in docs/security-model.md
# as a dedicated, labelled gate so CI can require them explicitly.
invariants:
	$(GO) test ./internal/invariants/...

clean:
	rm -rf $(BIN)

# snap builds the strict snap with snapcraft (requires Ubuntu + snapcraft).
snap:
	snapcraft pack --verbosity=brief

# selftest-dev runs the confinement self-test as a plain (unconfined) binary;
# the "blocked" checks are expected to fail outside the snap.
selftest-dev: build
	./$(BIN)/maat selftest
