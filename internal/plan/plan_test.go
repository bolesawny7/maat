package plan

import (
	"strings"
	"testing"
)

func TestNewProposalValid(t *testing.T) {
	p, err := NewProposal("service.restart", Params{"unit": "nginx.service"},
		[]string{"ev_abc123"}, "nginx is in a failed state after a transient dependency error", Proposer{Client: "test", Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Schema != SchemaVersion || p.ID == "" {
		t.Errorf("schema/id not set: %+v", p)
	}
	// Safety steps come from the catalog, not the caller.
	if len(p.Preconditions) == 0 || len(p.Verify) == 0 {
		t.Error("catalog-derived safety steps missing")
	}
	if p.Verify[0].Equals != "active" {
		t.Errorf("verify step wrong: %+v", p.Verify[0])
	}
	if p.Rollback.Kind != "none" {
		t.Errorf("rollback wrong: %+v", p.Rollback)
	}
}

func TestNewProposalRejectsUnknownKind(t *testing.T) {
	if _, err := NewProposal("service.delete_everything", Params{}, nil, "", Proposer{}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestNewProposalRejectsBadParams(t *testing.T) {
	cases := []struct {
		kind   string
		params Params
	}{
		{"service.restart", Params{}},                                  // missing unit
		{"service.restart", Params{"unit": "nginx"}},                   // no suffix
		{"service.restart", Params{"unit": "a b.service"}},             // space
		{"service.restart", Params{"unit": "x.service", "foo": "bar"}}, // unknown param
		{"snap.revert", Params{"snap": "Bad_Name"}},                    // invalid snap name
		{"snap.revert", Params{"snap": "good", "to_revision": "abc"}},  // bad revision
	}
	for _, c := range cases {
		if _, err := NewProposal(c.kind, c.params, nil, "", Proposer{}); err == nil {
			t.Errorf("expected error for %s %v", c.kind, c.params)
		}
	}
}

func TestUnitNameValidationRejectsInjection(t *testing.T) {
	// Shell/path metacharacters must not pass unit-name validation.
	bad := []string{"nginx.service; rm -rf /", "../../etc/passwd", "a|b.service", "$(id).service", "a`b`.service"}
	for _, u := range bad {
		if err := validateUnitName(u); err == nil {
			t.Errorf("validateUnitName accepted dangerous input %q", u)
		}
	}
	good := []string{"nginx.service", "systemd-networkd.service", "getty@tty1.service", "dev-sda.mount"}
	for _, u := range good {
		if err := validateUnitName(u); err != nil {
			t.Errorf("validateUnitName rejected valid %q: %v", u, err)
		}
	}
}

func TestResearchActionsFlagged(t *testing.T) {
	if !Catalog["netplan.try"].Research {
		t.Error("netplan.try must be flagged as research so the production actuator refuses it")
	}
	if Catalog["service.restart"].Research {
		t.Error("service.restart must not be research")
	}
}

func TestValidateRoundTrip(t *testing.T) {
	p, err := NewProposal("snap.revert", Params{"snap": "lxd"}, []string{"ev_1"}, "bad revision", Proposer{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Validate(); err != nil {
		t.Fatalf("round-tripped plan failed validation: %v", err)
	}
	if !strings.Contains(string(data), "snap.revert") {
		t.Error("kind missing from serialized plan")
	}
}

func TestKindsSorted(t *testing.T) {
	ks := Kinds()
	for i := 1; i < len(ks); i++ {
		if ks[i-1] > ks[i] {
			t.Fatalf("Kinds not sorted: %v", ks)
		}
	}
}
