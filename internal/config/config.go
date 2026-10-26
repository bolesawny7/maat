// Package config resolves where maat keeps its writable state and loads
// user-tunable settings. It prefers the snap's per-user common directory so that
// the plan store and audit trail survive snap refreshes and reverts.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DataDir returns maat's writable data directory, in order of preference:
//
//  1. $MAAT_DATA, if set (used by tests and the benchmark);
//  2. $SNAP_USER_COMMON, the snap's per-user common dir, which is not rolled
//     back by snap refresh/revert, so the trail persists across updates;
//  3. ~/snap/maat/common, if it exists — this is the same directory as (2), seen
//     from outside the snap, so maatctl finds the plans the confined server
//     wrote;
//  4. $XDG_STATE_HOME/maat or ~/.local/state/maat otherwise.
func DataDir() string {
	if d := os.Getenv("MAAT_DATA"); d != "" {
		return d
	}
	if d := os.Getenv("SNAP_USER_COMMON"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "maat")
	}
	// Bridge the snap's data dir to the unconfined maatctl.
	snapCommon := filepath.Join(home, "snap", "maat", "common")
	if fi, err := os.Stat(snapCommon); err == nil && fi.IsDir() {
		return snapCommon
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "maat")
	}
	return filepath.Join(home, ".local", "state", "maat")
}

// PlansDir is where proposed plans are stored.
func PlansDir() string { return filepath.Join(DataDir(), "plans") }

// TrailPath is the audit-trail file.
func TrailPath() string { return filepath.Join(DataDir(), "trail", "trail.jsonl") }

// ConfigPath is the user configuration file.
func ConfigPath() string { return filepath.Join(DataDir(), "config.yaml") }

// Config holds user-tunable settings. All fields are optional.
type Config struct {
	// RedactExtra are additional regexp patterns to redact, beyond the built-in
	// rules. Invalid patterns are reported by Load.
	RedactExtra []string `yaml:"redact_extra"`
	// MaxItems and MaxChars override the default output budget when non-zero.
	MaxItems int `yaml:"max_items"`
	MaxChars int `yaml:"max_chars"`
}

// Load reads the config file if present. A missing file yields a zero Config and
// no error, so maat works out of the box.
func Load() (Config, error) {
	var c Config
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}
