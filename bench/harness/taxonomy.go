package main

import (
	"fmt"
	"strings"
)

// Taxonomy is maat-bench's closed set of root-cause labels (bench/README.md).
// Every agent sees the same list, and a diagnosis scores only if it uses one.
var Taxonomy = []struct{ Label, Meaning string }{
	{"service.bad_exec", "a unit's ExecStart points at a missing or non-executable file"},
	{"service.transient_failure", "a unit failed once for a reason that has since passed; a restart fixes it"},
	{"service.stale_failure", "a unit is still marked failed although its cause is gone (for example its unit file was removed); clearing the failed state fixes it"},
	{"config.missing_file", "a required configuration file is missing"},
	{"config.syntax_error", "a configuration file can't be parsed"},
	{"resource.disk_full", "a filesystem is out of space"},
	{"resource.inodes_exhausted", "a filesystem is out of inodes"},
	{"resource.memory_oom", "a process was killed for exceeding a memory limit"},
	{"resource.cpu_saturation", "a runaway process saturates the CPU"},
	{"network.dns_misconfigured", "name resolution points at a wrong or unreachable server"},
	{"network.port_conflict", "two services want the same port"},
	{"network.bad_route", "the default route is missing or wrong"},
	{"permissions.wrong_owner", "a service can't access its data because of ownership or mode"},
	{"security.apparmor_denial", "an AppArmor profile blocks something a service needs"},
	{"packages.dpkg_interrupted", "a package operation was interrupted and left dpkg broken"},
	{"timer.failing_job", "a systemd timer's job fails every time it runs"},
	{"snap.bad_revision", "a snap was updated to a revision that doesn't work"},
	{"healthy", "nothing is wrong, even if the logs are noisy"},
}

func inTaxonomy(label string) bool {
	for _, t := range Taxonomy {
		if t.Label == label {
			return true
		}
	}
	return false
}

// taxonomyPrompt renders the labels for a task prompt.
func taxonomyPrompt() string {
	var b strings.Builder
	for _, t := range Taxonomy {
		fmt.Fprintf(&b, "- %s: %s\n", t.Label, t.Meaning)
	}
	return b.String()
}
