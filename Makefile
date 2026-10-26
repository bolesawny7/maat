# maat developer Makefile. Targets assume Go 1.26+ and, for the snap, snapcraft.

GO ?= go
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BIN := bin
# Architecture of the benchmark host (LXD containers run the same).
BENCH_ARCH ?= $(shell $(GO) env GOARCH)

.PHONY: all build test race fuzz vet fmt invariants clean snap bench-bin selftest-dev

all: build test vet invariants

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/maat ./cmd/maat
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/maatctl ./cmd/maatctl

# bench-bin builds what the benchmark pushes into each LXD container: a linux
# maat, and a maatctl built with the maatbench tag, whose confirmation comes from
# the harness. Never install the maatbench maatctl anywhere else.
bench-bin:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(BENCH_ARCH) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/linux/maat ./cmd/maat
	CGO_ENABLED=0 GOOS=linux GOARCH=$(BENCH_ARCH) $(GO) build -tags maatbench -ldflags "$(LDFLAGS)" -o $(BIN)/linux/maatctl-bench ./cmd/maatctl
	$(GO) build -o $(BIN)/harness ./bench/harness

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
	$(GO) test ./internal/collect/ -run '^$$' -fuzz '^FuzzParseMountinfo$$' -fuzztime=10s
	$(GO) test ./internal/condense/ -run '^$$' -fuzz '^FuzzRedact$$' -fuzztime=10s
	$(GO) test ./internal/condense/ -run '^$$' -fuzz '^FuzzDrainAdd$$' -fuzztime=10s
	$(GO) test ./internal/plan/ -run '^$$' -fuzz '^FuzzPlanValidate$$' -fuzztime=10s
	$(GO) test ./internal/termsafe/ -run '^$$' -fuzz '^FuzzText$$' -fuzztime=10s

vet:
	$(GO) vet ./...
	$(GO) vet -tags maatbench ./cmd/maatctl

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
# the "blocked" checks are informational outside the snap.
selftest-dev: build
	./$(BIN)/maat selftest
