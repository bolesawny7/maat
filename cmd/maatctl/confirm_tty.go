//go:build !maatbench

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// approver collects the human's confirmation and hosts the polkit agent. In
// release builds both use the controlling terminal.
type approver struct {
	tty *os.File
}

// newApprover opens the controlling terminal. Reading /dev/tty rather than
// standard input means a non-interactive caller, such as an agent piping input,
// cannot answer on the user's behalf.
func newApprover() (*approver, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("a terminal is required to confirm (could not open /dev/tty): %w", err)
	}
	return &approver{tty: tty}, nil
}

// confirm requires the user to type the plan ID on the terminal.
func (a *approver) confirm(planID string) (bool, error) {
	fmt.Fprintf(a.tty, "Type the plan ID to confirm, or anything else to abort:\n  %s\n> ", planID)
	line, err := bufio.NewReader(a.tty).ReadString('\n')
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == planID, nil
}

// startAgent starts a polkit text agent on the terminal for the privileged
// call. If none can be started, polkit can still use a desktop agent or a rule.
func (a *approver) startAgent() func() {
	stop, err := startPolkitAgent(a.tty)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: no polkit text agent (%v); polkit will use a desktop agent or a rule if one applies\n", err)
	}
	return stop
}

func (a *approver) close() { a.tty.Close() }
