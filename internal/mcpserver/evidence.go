package mcpserver

import (
	"sync"

	"github.com/bolesawny7/maat/internal/condense"
	"github.com/bolesawny7/maat/internal/obs"
)

// evidenceExcerptBytes bounds each excerpt kept for a plan's reviewer.
const evidenceExcerptBytes = 300

// evidenceLog remembers what every evidence ID this server session returned
// referred to, so propose_action can attach the cited evidence to the plan and
// say which citations the server never issued. It is bounded: the oldest
// entries are forgotten first.
type evidenceLog struct {
	mu    sync.Mutex
	max   int
	refs  map[string]obs.EvidenceRef
	order []string
}

func newEvidenceLog(max int) *evidenceLog {
	return &evidenceLog{max: max, refs: map[string]obs.EvidenceRef{}}
}

func (l *evidenceLog) add(id, source, subject, excerpt string) {
	if l == nil || id == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, seen := l.refs[id]; !seen {
		l.order = append(l.order, id)
		for len(l.order) > l.max {
			delete(l.refs, l.order[0])
			l.order = l.order[1:]
		}
	}
	l.refs[id] = obs.EvidenceRef{
		ID:      id,
		Source:  source,
		Subject: condense.Clip(subject, 200),
		Excerpt: obs.Untrusted(condense.Clip(excerpt, evidenceExcerptBytes)),
	}
}

// resolve splits cited IDs into those this session issued and those it didn't.
func (l *evidenceLog) resolve(ids []string) (found []obs.EvidenceRef, missing []string) {
	if l == nil {
		return nil, ids
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range ids {
		if r, ok := l.refs[id]; ok {
			found = append(found, r)
		} else {
			missing = append(missing, id)
		}
	}
	return found, missing
}
