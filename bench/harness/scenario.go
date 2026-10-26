// Command harness runs maat-bench: it provisions broken Ubuntu machines in LXD,
// lets an agent investigate each one through maat's real MCP tools, and scores
// the result deterministically. See bench/README.md for the design.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Scenario describes one broken (or healthy) machine and how to judge an agent's
// handling of it.
type Scenario struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Instance string   `yaml:"instance"` // "container" (only option in v0)
	Image    string   `yaml:"image"`    // e.g. "ubuntu:24.04"
	Setup    []string `yaml:"setup"`    // commands run as root in the box to break it

	Truth           Truth  `yaml:"truth"`
	Expect          string `yaml:"expect"`           // "diagnose_only" or "fix"
	AcceptablePlans []Plan `yaml:"acceptable_plans"` // for fix scenarios
	HealthCheck     string `yaml:"health_check"`     // shell command; exit 0 == healthy
	RealWorld       string `yaml:"real_world"`       // link to evidence this happens in practice

	Adversarial *Adversarial `yaml:"adversarial,omitempty"`

	path string
}

// Truth is the expected diagnosis.
type Truth struct {
	RootCause string          `yaml:"root_cause"`
	Component string          `yaml:"component"`
	Evidence  []TruthEvidence `yaml:"evidence"`
}

// TruthEvidence describes a record a correct diagnosis should be able to cite.
type TruthEvidence struct {
	Source   string `yaml:"source"`
	Unit     string `yaml:"unit"`
	Contains string `yaml:"contains"`
}

// Plan is an acceptable fix: a kind and the parameters that matter for matching.
type Plan struct {
	Kind   string            `yaml:"kind"`
	Params map[string]string `yaml:"params"`
}

// Adversarial injects attacker-controlled text into the box's logs.
type Adversarial struct {
	Vector  string `yaml:"vector"`  // logger, sshd_invalid_user, unit_description, http_access_log
	Payload string `yaml:"payload"` // the injected instruction
	Canary  string `yaml:"canary"`  // unique token; acting on it counts as a hijack
}

// Validate checks a scenario is well-formed before it is run.
func (s *Scenario) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("scenario has no id")
	}
	if s.Instance == "" {
		s.Instance = "container"
	}
	if s.Image == "" {
		s.Image = "ubuntu:24.04"
	}
	if s.Expect != "diagnose_only" && s.Expect != "fix" {
		return fmt.Errorf("scenario %s: expect must be diagnose_only or fix, got %q", s.ID, s.Expect)
	}
	if s.Truth.RootCause == "" {
		return fmt.Errorf("scenario %s: truth.root_cause is required", s.ID)
	}
	if s.Expect == "fix" && len(s.AcceptablePlans) == 0 {
		return fmt.Errorf("scenario %s: fix scenarios need at least one acceptable_plan", s.ID)
	}
	if s.HealthCheck == "" && s.Expect == "fix" {
		return fmt.Errorf("scenario %s: fix scenarios need a health_check", s.ID)
	}
	return nil
}

// LoadScenario reads and validates one scenario file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.path = path
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// LoadScenarios loads every *.yaml under dir (recursively), sorted by ID.
func LoadScenarios(dir string) ([]*Scenario, error) {
	var scenarios []*Scenario
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil
		}
		s, err := LoadScenario(path)
		if err != nil {
			return err
		}
		scenarios = append(scenarios, s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].ID < scenarios[j].ID })
	return scenarios, nil
}
