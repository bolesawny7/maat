//go:build maatbench

package main

import (
	"fmt"
	"os"
)

// benchBanner is printed by every benchmark build. The invariant tests also look
// for it to prove release binaries don't contain this file.
const benchBanner = "maatbench build: confirmation supplied by the benchmark harness, not a person"

// approver, in benchmark builds only, takes confirmation from the harness: the
// plan ID to approve is passed in MAAT_BENCH_CONFIRM. These builds run inside
// throwaway LXD containers, where a scoped polkit rule authorizes the bench user
// instead of an interactive agent.
type approver struct{}

func newApprover() (*approver, error) {
	fmt.Fprintln(os.Stderr, benchBanner)
	return &approver{}, nil
}

func (a *approver) confirm(planID string) (bool, error) {
	return os.Getenv("MAAT_BENCH_CONFIRM") == planID, nil
}

func (a *approver) startAgent() func() { return func() {} }

func (a *approver) close() {}
