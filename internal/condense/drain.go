package condense

import (
	"regexp"
	"strings"
)

// Drain groups log messages into templates using a fixed-depth parse tree, the
// online algorithm of He et al., "Drain: An Online Log Parsing Approach with
// Fixed Depth Tree" (IEEE ICWS 2017). Messages are first masked for obvious
// variables (numbers, IPs, hex, UUIDs), then routed by token count and by their
// leading tokens to a small set of candidate clusters, where the best match by
// token similarity is updated or a new cluster is created.
//
// Drain is deterministic: the same sequence of inputs always yields the same
// clusters, which matters for a benchmark that must be reproducible.
type Drain struct {
	depth        int     // total tree depth; internal token layers = depth-2
	simThreshold float64 // minimum token similarity to join a cluster
	maxChildren  int     // cap on children per internal node
	maxClusters  int     // global cap on distinct clusters, to bound memory
	maskers      []masker

	root     map[int]*drainNode // keyed by token count
	clusters []*Cluster         // all clusters, in creation order (stable IDs)
	overflow *Cluster           // catch-all once maxClusters is reached
}

const wildcard = "<*>"

type masker struct {
	re   *regexp.Regexp
	repl string
}

type drainNode struct {
	children map[string]*drainNode
	clusters []*Cluster
}

func newDrainNode() *drainNode {
	return &drainNode{children: map[string]*drainNode{}}
}

// Cluster is a discovered log template together with the running count of
// messages assigned to it and a representative example (the first message seen).
// Drain tracks only identity and frequency here; the summary layer attaches
// timing and severity in its own structures keyed by cluster ID.
type Cluster struct {
	ID      int
	Tokens  []string // template tokens, with wildcard for variable positions
	Count   int
	Example string // first raw message that created the cluster
}

// Default global cap on distinct clusters. Real hosts produce well under this;
// the cap only bounds memory against adversarial or pathological log streams
// (e.g. a flood of unique, un-maskable lines). Excess lines fold into a single
// overflow cluster.
const defaultMaxClusters = 4000

// NewDrain returns a Drain with maat's defaults: depth 4, similarity 0.4, up to
// 100 children per node, and maskers for the variable shapes common in Linux
// logs. These defaults follow the paper and the widely used Drain3 settings.
func NewDrain() *Drain {
	return &Drain{
		depth:        4,
		simThreshold: 0.4,
		maxChildren:  100,
		maxClusters:  defaultMaxClusters,
		maskers:      defaultMaskers(),
		root:         map[int]*drainNode{},
	}
}

func defaultMaskers() []masker {
	return []masker{
		{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), wildcard}, // UUID
		{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`), wildcard},                                 // IPv4 (+optional port)
		{regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`), wildcard},                             // MAC
		{regexp.MustCompile(`0x[0-9A-Fa-f]+`), wildcard},                                                       // hex literal
		{regexp.MustCompile(`\b[0-9A-Fa-f]{12,}\b`), wildcard},                                                 // long hex blob
		{regexp.MustCompile(`\b\d+(?:\.\d+)?\b`), wildcard},                                                    // numbers
	}
}

// preprocess masks variable substrings, then splits on whitespace.
func (d *Drain) preprocess(msg string) []string {
	for _, m := range d.maskers {
		msg = m.re.ReplaceAllString(msg, m.repl)
	}
	return strings.Fields(msg)
}

// firstNonWildcard returns the key for an internal tree layer: the token at
// position i, or a stable sentinel if it is a wildcard or missing. Routing on
// the literal leading tokens is what keeps candidate lists short.
func layerKey(tokens []string, i int) string {
	if i >= len(tokens) {
		return "∅"
	}
	t := tokens[i]
	if t == wildcard || containsDigit(t) {
		return "*"
	}
	return t
}

func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// Add assigns one message to a cluster, creating one if needed, and returns it.
func (d *Drain) Add(msg string) *Cluster {
	tokens := d.preprocess(msg)
	n := len(tokens)

	lengthNode, ok := d.root[n]
	if !ok {
		lengthNode = newDrainNode()
		d.root[n] = lengthNode
	}

	// Descend the fixed-depth tree over the leading tokens.
	node := lengthNode
	internalLayers := d.depth - 2
	if internalLayers < 0 {
		internalLayers = 0
	}
	for i := 0; i < internalLayers && i < n; i++ {
		key := layerKey(tokens, i)
		child, ok := node.children[key]
		if !ok {
			// Respect maxChildren: once full, funnel the rest through "*".
			if len(node.children) >= d.maxChildren {
				key = "*"
				child, ok = node.children[key]
			}
			if !ok {
				child = newDrainNode()
				node.children[key] = child
			}
		}
		node = child
	}

	cluster, sim := d.bestMatch(node.clusters, tokens)
	if cluster != nil && sim >= d.simThreshold {
		mergeTemplate(cluster, tokens)
		cluster.Count++
		return cluster
	}

	// Bound memory: once the global cap is reached, fold further unmatched lines
	// into a single overflow cluster instead of growing without limit.
	if d.maxClusters > 0 && len(d.clusters) >= d.maxClusters {
		if d.overflow == nil {
			d.overflow = &Cluster{ID: len(d.clusters) + 1, Tokens: []string{"<overflow>"}, Example: msg}
			d.clusters = append(d.clusters, d.overflow)
		}
		d.overflow.Count++
		return d.overflow
	}

	// No good match: create a new cluster seeded with these tokens.
	nc := &Cluster{
		ID:      len(d.clusters) + 1,
		Tokens:  append([]string(nil), tokens...),
		Count:   1,
		Example: msg,
	}
	d.clusters = append(d.clusters, nc)
	node.clusters = append(node.clusters, nc)
	return nc
}

// bestMatch returns the cluster in cands most similar to tokens, and the
// similarity. Similarity is the fraction of positions whose tokens are equal,
// with a wildcard position counting as a match (as in the paper). Ties favor
// the cluster with fewer wildcards, i.e. the more specific template.
func (d *Drain) bestMatch(cands []*Cluster, tokens []string) (*Cluster, float64) {
	var best *Cluster
	bestSim := -1.0
	bestParams := 1 << 30
	for _, c := range cands {
		if len(c.Tokens) != len(tokens) {
			continue
		}
		sim, params := seqSim(c.Tokens, tokens)
		if sim > bestSim || (sim == bestSim && params < bestParams) {
			best, bestSim, bestParams = c, sim, params
		}
	}
	if best == nil {
		return nil, 0
	}
	return best, bestSim
}

// seqSim returns the token similarity of a template and a message of equal
// length, plus the number of wildcard positions in the template.
func seqSim(tmpl, msg []string) (float64, int) {
	if len(tmpl) == 0 {
		return 1, 0
	}
	same := 0
	params := 0
	for i := range tmpl {
		if tmpl[i] == wildcard {
			params++
			same++ // a wildcard matches anything
			continue
		}
		if tmpl[i] == msg[i] {
			same++
		}
	}
	return float64(same) / float64(len(tmpl)), params
}

// mergeTemplate widens the cluster's template: any position where the template
// and the new message disagree becomes a wildcard.
func mergeTemplate(c *Cluster, tokens []string) {
	for i := range c.Tokens {
		if c.Tokens[i] != wildcard && c.Tokens[i] != tokens[i] {
			c.Tokens[i] = wildcard
		}
	}
}

// Clusters returns all discovered clusters in creation order.
func (d *Drain) Clusters() []*Cluster { return d.clusters }

// Template renders a cluster's tokens back into a single string.
func (c *Cluster) Template() string { return strings.Join(c.Tokens, " ") }

// numWildcards reports how many variable positions the template has, used as a
// readability signal when ranking.
func (c *Cluster) numWildcards() int {
	n := 0
	for _, t := range c.Tokens {
		if t == wildcard {
			n++
		}
	}
	return n
}
