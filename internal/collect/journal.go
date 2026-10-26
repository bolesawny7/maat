// Package collect reads system state from an Ubuntu host: the journal, systemd
// units, processes, filesystems, networking, and AppArmor denials.
//
// Every collector is split in two: a pure parser that turns captured bytes into
// typed records (unit-tested on any platform with fixtures) and a thin live
// wrapper that gathers those bytes from the running system (exercised in a VM).
// Live wrappers never pass untrusted data through a shell; they exec programs
// with fixed argument lists or read files directly.
package collect

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

// JournalOptions selects which journal entries to read. All fields are
// optional; the zero value reads the most recent entries from the current boot.
type JournalOptions struct {
	Unit     string        // restrict to one systemd unit
	Priority obs.Severity  // most-verbose severity to include (<= this number)
	Since    time.Duration // how far back from now; 0 means no lower bound
	Lines    int           // cap on number of entries; 0 selects a default
	Kernel   bool          // only kernel messages (journalctl -k)
	ThisBoot bool          // restrict to the current boot (journalctl -b)
}

const defaultJournalLines = 2000

// journalctlArgs builds the fixed argument list for journalctl. It is separated
// from execution so tests can assert that untrusted input never becomes a flag
// in an unexpected position.
func journalctlArgs(o JournalOptions) []string {
	args := []string{"--output=json", "--no-pager"}
	if o.Kernel {
		args = append(args, "-k")
	}
	if o.ThisBoot {
		args = append(args, "-b")
	}
	if o.Unit != "" {
		args = append(args, "--unit", o.Unit)
	}
	// A positive Priority selects "this urgency and more urgent". The zero value
	// means "no priority filter": it coincides with SevEmergency, but filtering to
	// emergency-only is not a use case maat needs, so zero is treated as unset.
	if o.Priority > 0 {
		args = append(args, "--priority", strconv.Itoa(int(o.Priority)))
	}
	if o.Since > 0 {
		args = append(args, "--since", fmt.Sprintf("-%ds", int(o.Since.Seconds())))
	}
	lines := o.Lines
	if lines <= 0 {
		lines = defaultJournalLines
	}
	args = append(args, "--lines", strconv.Itoa(lines))
	return args
}

// ReadJournal runs journalctl with the given options and parses the result. It
// requires the host journal to be readable, which the snap obtains through the
// log-observe interface. The journalctl binary path is fixed.
func ReadJournal(ctx context.Context, o JournalOptions) ([]obs.LogRecord, error) {
	cmd := exec.CommandContext(ctx, "journalctl", journalctlArgs(o)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start journalctl: %w", err)
	}
	recs, parseErr := ParseJournalJSON(out)
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, fmt.Errorf("parse journal: %w", parseErr)
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		// Turn the common permission failure into an actionable hint.
		if strings.Contains(msg, "insufficient permissions") || strings.Contains(msg, "Failed to open files") {
			return nil, fmt.Errorf("%s (grant journal access: connect the log-observe interface and add the user to the systemd-journal group)", msg)
		}
		return nil, fmt.Errorf("journalctl: %s", msg)
	}
	return recs, nil
}

// journalEntry mirrors the subset of journald's JSON export we use. Fields can
// arrive as strings or, for MESSAGE, as an array of byte values when the entry
// is not valid UTF-8, so they are decoded as json.RawMessage and interpreted by
// fieldString.
type journalEntry struct {
	Realtime    json.RawMessage `json:"__REALTIME_TIMESTAMP"`
	Cursor      json.RawMessage `json:"__CURSOR"`
	Message     json.RawMessage `json:"MESSAGE"`
	Priority    json.RawMessage `json:"PRIORITY"`
	SystemdUnit json.RawMessage `json:"_SYSTEMD_UNIT"`
	Unit        json.RawMessage `json:"UNIT"`
	Identifier  json.RawMessage `json:"SYSLOG_IDENTIFIER"`
	Hostname    json.RawMessage `json:"_HOSTNAME"`
	Transport   json.RawMessage `json:"_TRANSPORT"`
}

// ParseJournalJSON parses journald's newline-delimited JSON export into log
// records. Malformed lines are skipped rather than failing the whole read, so a
// single corrupt entry can't blind the agent to everything else.
func ParseJournalJSON(r io.Reader) ([]obs.LogRecord, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	var recs []obs.LogRecord
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e journalEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		rec := obs.LogRecord{
			Source:   "journal",
			Message:  fieldString(e.Message),
			Hostname: fieldString(e.Hostname),
			Cursor:   fieldString(e.Cursor),
			Priority: parsePriority(fieldString(e.Priority)),
		}
		if u := fieldString(e.SystemdUnit); u != "" {
			rec.Unit = u
		} else if u := fieldString(e.Unit); u != "" {
			rec.Unit = u
		} else {
			rec.Unit = fieldString(e.Identifier)
		}
		if ts := fieldString(e.Realtime); ts != "" {
			if usec, err := strconv.ParseInt(ts, 10, 64); err == nil {
				rec.Time = time.UnixMicro(usec).UTC()
			}
		}
		if fieldString(e.Transport) == "kernel" {
			rec.Source = "kernel"
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return recs, err
	}
	return recs, nil
}

// fieldString interprets a journald field that may be a JSON string or an array
// of byte values (used for non-UTF-8 payloads). Anything else yields "".
func fieldString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var bytesArr []int
	if err := json.Unmarshal(raw, &bytesArr); err == nil {
		b := make([]byte, len(bytesArr))
		for i, v := range bytesArr {
			b[i] = byte(v)
		}
		return string(b)
	}
	return ""
}

// parsePriority maps journald's PRIORITY string to a Severity, defaulting to
// informational when absent or unparseable.
func parsePriority(s string) obs.Severity {
	if s == "" {
		return obs.SevInfo
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 7 {
		return obs.SevInfo
	}
	return obs.Severity(n)
}
