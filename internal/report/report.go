// Package report produces maat's deterministic, AI-free diagnosis: the same
// collectors and condense pipeline the MCP tools use, rendered as a plain text
// summary. It is what `maat diagnose` prints and a useful baseline the benchmark
// scores models against.
package report

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/bolesawny7/maat/internal/collect"
	"github.com/bolesawny7/maat/internal/condense"
	"github.com/bolesawny7/maat/internal/obs"
)

// Run gathers system state and writes a human-readable report to w. It never
// involves a model. Collection errors are reported inline rather than aborting,
// so a partial report is still useful.
func Run(ctx context.Context, w io.Writer, red *condense.Redactor) {
	if red == nil {
		red = condense.NewRedactor()
	}
	fmt.Fprintln(w, "maat diagnosis")
	fmt.Fprintln(w, "==============")

	// Overview.
	failed, _ := collect.ListUnits(ctx, true)
	ov := collect.CollectOverview("/", len(failed))
	fmt.Fprintf(w, "\nHost: %s\nKernel: %s\nUptime: %s   Load: %.2f %.2f %.2f\nMemory: %.0f%% used (%d/%d MB)\n",
		ov.OSRelease, ov.KernelRelease, ov.Uptime, ov.Load1, ov.Load5, ov.Load15,
		ov.MemUsedPct, ov.MemTotalMB-ov.MemAvailMB, ov.MemTotalMB)

	// Failed units.
	fmt.Fprintf(w, "\nFailed units: %d\n", len(failed))
	for _, u := range failed {
		fmt.Fprintf(w, "  - %s: %s/%s — %s\n", u.Name, u.ActiveState, u.SubState, u.Description)
	}

	// Disk pressure.
	if disks, err := collect.CollectDisks("/"); err == nil {
		var warn []collect.DiskUsage
		for _, d := range disks {
			if d.UsedPct >= 90 || d.InodesUsedPct >= 90 {
				warn = append(warn, d)
			}
		}
		if len(warn) > 0 {
			fmt.Fprintf(w, "\nFilesystems near full:\n")
			for _, d := range warn {
				fmt.Fprintf(w, "  - %s (%s): %.1f%% space, %.1f%% inodes\n", d.MountPoint, d.FSType, d.UsedPct, d.InodesUsedPct)
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
				fmt.Fprintf(w, "  - %s %s on %s (x%d)\n", d.Profile, d.Operation, d.Name, d.Count)
			}
		}
	}

	// Error-and-worse journal templates.
	recs, err := collect.ReadJournal(ctx, collect.JournalOptions{Priority: obs.SevError, Since: 6 * time.Hour, ThisBoot: true})
	if err != nil {
		fmt.Fprintf(w, "\nJournal: could not read (%v)\n", err)
		return
	}
	sum := condense.Summarize(recs, red, condense.NewDrain(), obs.Budget{MaxItems: 15})
	fmt.Fprintf(w, "\nTop error log templates (%d entries across %d templates, %d redactions):\n",
		sum.TotalRecords, sum.TotalTemplates, sum.Redactions)
	for _, tmpl := range sum.Templates {
		unit := ""
		if len(tmpl.Units) > 0 {
			unit = " [" + tmpl.Units[0] + "]"
		}
		fmt.Fprintf(w, "  - (x%d, %s)%s %s\n", tmpl.Count, tmpl.MaxPriority, unit, tmpl.Template)
	}
}
