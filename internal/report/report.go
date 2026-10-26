// Package report produces maat's deterministic, AI-free diagnosis: the same
// collectors and condense pipeline the MCP tools use, rendered as a plain text
// summary. It is what `maat diagnose` prints.
package report

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/bolesawny7/maat/internal/collect"
	"github.com/bolesawny7/maat/internal/condense"
	"github.com/bolesawny7/maat/internal/obs"
	"github.com/bolesawny7/maat/internal/termsafe"
)

// Run gathers system state and writes a human-readable report to w. It never
// involves a model. Collection errors are reported inline rather than aborting,
// so a partial report is still useful. Text that came from logs, units or file
// names is redacted and made safe for a terminal before it is printed.
func Run(ctx context.Context, w io.Writer, red *condense.Redactor) {
	if red == nil {
		red = condense.NewRedactor()
	}
	safe := func(s obs.Untrusted) string {
		r, _ := red.Redact(string(s))
		return termsafe.Line(condense.Clip(r, condense.MaxFieldBytes))
	}
	fmt.Fprintln(w, "maat diagnosis")
	fmt.Fprintln(w, "==============")

	// Overview.
	failed, unitsErr := collect.ListUnits(ctx, true)
	ov := collect.CollectOverview("/", len(failed))
	fmt.Fprintf(w, "\nHost: %s\nKernel: %s\nUptime: %s   Load: %.2f %.2f %.2f\nMemory: %.0f%% used (%d/%d MB)\n",
		termsafe.Line(ov.OSRelease), termsafe.Line(ov.KernelRelease), ov.Uptime, ov.Load1, ov.Load5, ov.Load15,
		ov.MemUsedPct, ov.MemTotalMB-ov.MemAvailMB, ov.MemTotalMB)

	// Failed units.
	if unitsErr != nil {
		fmt.Fprintf(w, "\nFailed units: could not list units (%s)\n", termsafe.Line(unitsErr.Error()))
	} else {
		fmt.Fprintf(w, "\nFailed units: %d\n", len(failed))
		for _, u := range failed {
			fmt.Fprintf(w, "  - %s: %s/%s (load %s) — %s\n", termsafe.Line(u.Name), u.ActiveState, u.SubState, u.LoadState, safe(u.Description))
		}
	}

	// Disk pressure.
	if disks, err := collect.CollectDisks("/"); err == nil {
		var warn []collect.DiskUsage
		for _, d := range disks {
			if !d.ReadOnly && (d.UsedPct >= 90 || d.InodesUsedPct >= 90) {
				warn = append(warn, d)
			}
		}
		if len(warn) > 0 {
			fmt.Fprintf(w, "\nFilesystems near full:\n")
			for _, d := range warn {
				fmt.Fprintf(w, "  - %s (%s): %.1f%% space, %.1f%% inodes\n", termsafe.Line(d.MountPoint), d.FSType, d.UsedPct, d.InodesUsedPct)
			}
		}
	}

	// AppArmor denials from the kernel log.
	if krecs, err := collect.ReadJournal(ctx, collect.JournalOptions{Kernel: true, Since: 24 * time.Hour}); err == nil {
		denials := collect.AppArmorDenials(krecs)
		if len(denials) > 0 {
			fmt.Fprintf(w, "\nAppArmor denials: %d distinct\n", len(denials))
			for i, d := range denials {
				if i >= 10 {
					fmt.Fprintf(w, "  ...and %d more\n", len(denials)-10)
					break
				}
				fmt.Fprintf(w, "  - %s %s on %s (x%d)\n", termsafe.Line(d.Profile), d.Operation, safe(d.Name), d.Count)
			}
		}
	}

	// Error-and-worse journal templates.
	recs, err := collect.ReadJournal(ctx, collect.JournalOptions{Priority: obs.SevError, Since: 6 * time.Hour, ThisBoot: true})
	if err != nil {
		fmt.Fprintf(w, "\nJournal: could not read (%s)\n", termsafe.Line(err.Error()))
		return
	}
	sum := condense.Summarize(recs, red, condense.NewDrain(), obs.Budget{MaxItems: 15})
	fmt.Fprintf(w, "\nTop error log templates (%d entries across %d templates, %d redactions):\n",
		sum.TotalRecords, sum.TotalTemplates, sum.Redactions)
	for _, tmpl := range sum.Templates {
		unit := ""
		if len(tmpl.Units) > 0 {
			unit = " [" + termsafe.Line(tmpl.Units[0]) + "]"
		}
		fmt.Fprintf(w, "  - (x%d, %s)%s %s\n", tmpl.Count, tmpl.MaxPriority, unit, termsafe.Line(tmpl.Template))
	}
}
