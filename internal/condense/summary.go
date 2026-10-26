package condense

import (
	"math"
	"sort"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

// TemplateSummary is one ranked log template as a model sees it: a redacted
// example, how often it occurred, when, how severe, and a stable evidence ID
// the model can cite and a human can resolve back to the underlying entries.
type TemplateSummary struct {
	EvidenceID  string        `json:"evidence_id"`
	Template    string        `json:"template"`
	Example     obs.Untrusted `json:"example"`
	Count       int           `json:"count"`
	First       time.Time     `json:"first"`
	Last        time.Time     `json:"last"`
	MaxPriority obs.Severity  `json:"max_priority"`
	Units       []string      `json:"units,omitempty"`
}

// JournalSummary is the budgeted result of condensing a set of log records.
type JournalSummary struct {
	Templates      []TemplateSummary `json:"templates"`
	TotalRecords   int               `json:"total_records"`
	TotalTemplates int               `json:"total_templates"`
	Shown          int               `json:"shown"`
	Truncated      bool              `json:"truncated"`
	Redactions     int               `json:"redactions"`
	Window         Window            `json:"window"`
}

// Window is the time span the summarized records cover.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// clusterMeta holds the timing, severity, and unit information that Drain itself
// does not track, keyed by cluster ID.
type clusterMeta struct {
	first, last time.Time
	maxSev      obs.Severity
	units       map[string]struct{}
	example     string
}

// Summarize is the core of the condense pipeline. It redacts each record,
// clusters the messages into templates with Drain, ranks the templates, and
// trims the result to the budget. The input slice is not modified.
func Summarize(records []obs.LogRecord, r *Redactor, d *Drain, budget obs.Budget) JournalSummary {
	budget = budget.WithDefaults()
	if r == nil {
		r = NewRedactor()
	}
	if d == nil {
		d = NewDrain()
	}

	sum := JournalSummary{TotalRecords: len(records)}
	if len(records) == 0 {
		return sum
	}

	// Copy so we can sort and redact without touching the caller's slice.
	recs := append([]obs.LogRecord(nil), records...)
	obs.SortRecordsByTime(recs)
	sum.Window = Window{From: recs[0].Time, To: recs[len(recs)-1].Time}

	meta := map[int]*clusterMeta{}
	for i := range recs {
		red, n := r.Redact(recs[i].Message)
		sum.Redactions += n

		c := d.Add(red)
		m := meta[c.ID]
		if m == nil {
			m = &clusterMeta{first: recs[i].Time, last: recs[i].Time, maxSev: recs[i].Priority, units: map[string]struct{}{}, example: red}
			meta[c.ID] = m
		}
		if recs[i].Time.Before(m.first) {
			m.first = recs[i].Time
		}
		if recs[i].Time.After(m.last) {
			m.last = recs[i].Time
		}
		if recs[i].Priority.AtLeast(m.maxSev) { // more urgent = smaller number
			m.maxSev = recs[i].Priority
		}
		if recs[i].Unit != "" {
			m.units[recs[i].Unit] = struct{}{}
		}
	}

	clusters := d.Clusters()
	sum.TotalTemplates = len(clusters)

	summaries := make([]TemplateSummary, 0, len(clusters))
	for _, c := range clusters {
		m := meta[c.ID]
		if m == nil {
			continue
		}
		summaries = append(summaries, TemplateSummary{
			EvidenceID:  obs.EvidenceID("journal", c.Template()),
			Template:    c.Template(),
			Example:     obs.Untrusted(m.example),
			Count:       c.Count,
			First:       m.first,
			Last:        m.last,
			MaxPriority: m.maxSev,
			Units:       sortedKeys(m.units),
		})
	}

	rankTemplates(summaries, sum.Window)

	// Apply the budget: cap by item count, then by estimated size.
	kept := applyBudget(summaries, budget)
	sum.Templates = kept
	sum.Shown = len(kept)
	sum.Truncated = len(kept) < len(summaries)
	return sum
}

// rankTemplates orders templates by a score that favors severe, frequent, and
// recent events, so the most diagnostic lines survive the budget. The order is
// deterministic: ties fall back to the evidence ID.
func rankTemplates(ts []TemplateSummary, w Window) {
	span := w.To.Sub(w.From)
	if span <= 0 {
		span = time.Second
	}
	score := func(t TemplateSummary) float64 {
		// Severity weight: emergency..debug -> 8..1.
		sev := float64(8 - int(t.MaxPriority))
		if sev < 1 {
			sev = 1
		}
		// Recency in [0,1]: 1 if the last occurrence is at the window end.
		recency := 1.0
		if span > 0 {
			recency = float64(t.Last.Sub(w.From)) / float64(span)
		}
		// Frequency, dampened so a single flood doesn't bury everything else.
		freq := math.Log1p(float64(t.Count))
		return sev*3 + freq + recency*2
	}
	sort.SliceStable(ts, func(i, j int) bool {
		si, sj := score(ts[i]), score(ts[j])
		if si != sj {
			return si > sj
		}
		return ts[i].EvidenceID < ts[j].EvidenceID
	})
}

// applyBudget trims a ranked slice to the budget, counting an approximate
// rendered size per template.
func applyBudget(ts []TemplateSummary, b obs.Budget) []TemplateSummary {
	out := make([]TemplateSummary, 0, len(ts))
	chars := 0
	for i, t := range ts {
		if i >= b.MaxItems {
			break
		}
		cost := len(t.Template) + len(t.Example) + 60 // 60 ≈ fixed JSON overhead
		if chars+cost > b.MaxChars && len(out) > 0 {
			break
		}
		chars += cost
		out = append(out, t)
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
