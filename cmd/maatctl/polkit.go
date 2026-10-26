package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// pkttyagentPath is fixed rather than looked up in $PATH: the agent reads the
// admin password from the terminal, so a lookalike earlier in $PATH could
// capture it.
const pkttyagentPath = "/usr/bin/pkttyagent"

// startPolkitAgent starts a textual polkit authentication agent on tty, the way
// systemctl does, so the admin prompt can appear in an SSH session or on a
// console with no desktop agent. pkttyagent registers for its parent process
// (maatctl) only, and --fallback makes it defer to an agent the session already
// has, such as a desktop one. The returned function stops it.
func startPolkitAgent(tty *os.File) (stop func(), err error) {
	noop := func() {}
	if _, err := os.Stat(pkttyagentPath); err != nil {
		return noop, fmt.Errorf("%s not found (install the polkitd package, or authorize with a polkit rule)", pkttyagentPath)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return noop, err
	}
	defer r.Close()
	cmd := exec.Command(pkttyagentPath, "--notify-fd", "3", "--fallback")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.ExtraFiles = []*os.File{w} // becomes fd 3 in the agent
	if err := cmd.Start(); err != nil {
		w.Close()
		return noop, err
	}
	w.Close()

	// pkttyagent closes the notify fd once it has registered, or exits (closing
	// it too) if it can't; either way the read below returns.
	ready := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(ready)
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	}
	return func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	}, nil
}
