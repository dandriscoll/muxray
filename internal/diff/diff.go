// Package diff produces compact, LLM-friendly diffs between two cleaned pane
// texts. Unchanged content is detected by exact equality of the cleaned text,
// which makes `changed: false` fully deterministic.
package diff

import (
	"fmt"
	"strings"
)

// Options tunes diff output.
type Options struct {
	// Context is the number of unchanged lines to include around each hunk.
	Context int
	// Full disables the compact cap on added/removed lines.
	Full bool
	// MaxItems caps added and removed line counts in compact mode (per side).
	MaxItems int
	// TailLines sets the size of CurrentTailExcerpt.
	TailLines int
}

// Result is the structured diff. Field names match the directive's contract.
type Result struct {
	Changed            bool     `json:"changed"`
	Summary            string   `json:"summary"`
	Added              []string `json:"added"`
	Removed            []string `json:"removed"`
	Context            []string `json:"context,omitempty"`
	Hunks              int      `json:"hunks"`
	AddedCount         int      `json:"added_count"`
	RemovedCount       int      `json:"removed_count"`
	Truncated          bool     `json:"truncated"`
	CurrentTailExcerpt []string `json:"current_tail_excerpt"`
	PreviousSnapshot   string   `json:"previous_snapshot"`
	CurrentSnapshot    string   `json:"current_snapshot"`
}

// Compute diffs prev against cur (both cleaned text). When the texts are
// byte-identical it returns Changed=false deterministically with no diff work.
func Compute(prevID, curID, prev, cur string, o Options) Result {
	if o.TailLines <= 0 {
		o.TailLines = 10
	}
	r := Result{
		PreviousSnapshot:   prevID,
		CurrentSnapshot:    curID,
		Added:              []string{},
		Removed:            []string{},
		CurrentTailExcerpt: tail(cur, o.TailLines),
	}
	if prev == cur {
		r.Changed = false
		r.Summary = "no changes"
		return r
	}

	r.Changed = true
	hunks := computeHunks(splitLines(prev), splitLines(cur), o.Context)
	for _, h := range hunks {
		r.Added = append(r.Added, h.Added...)
		r.Removed = append(r.Removed, h.Removed...)
		if o.Context > 0 {
			r.Context = append(r.Context, h.Context...)
		}
	}
	r.Hunks = len(hunks)
	r.AddedCount = len(r.Added)
	r.RemovedCount = len(r.Removed)

	if !o.Full && o.MaxItems > 0 {
		if len(r.Added) > o.MaxItems {
			r.Added = r.Added[:o.MaxItems]
			r.Truncated = true
		}
		if len(r.Removed) > o.MaxItems {
			r.Removed = r.Removed[:o.MaxItems]
			r.Truncated = true
		}
	}

	r.Summary = fmt.Sprintf("+%d -%d lines, %d hunk(s)", r.AddedCount, r.RemovedCount, r.Hunks)
	return r
}

// Hunk is one contiguous region of change with surrounding context.
type Hunk struct {
	Added   []string
	Removed []string
	Context []string
}

type op struct {
	kind byte // '=', '+', '-'
	text string
}

func computeHunks(a, b []string, ctx int) []Hunk {
	ops := diffOps(a, b)
	var hunks []Hunk
	i := 0
	for i < len(ops) {
		if ops[i].kind == '=' {
			i++
			continue
		}
		start := i
		for i < len(ops) && ops[i].kind != '=' {
			i++
		}
		end := i
		h := Hunk{}
		for k := max(0, start-ctx); k < start; k++ {
			if ops[k].kind == '=' {
				h.Context = append(h.Context, ops[k].text)
			}
		}
		for k := start; k < end; k++ {
			if ops[k].kind == '+' {
				h.Added = append(h.Added, ops[k].text)
			} else {
				h.Removed = append(h.Removed, ops[k].text)
			}
		}
		for k := end; k < min(len(ops), end+ctx); k++ {
			if ops[k].kind == '=' {
				h.Context = append(h.Context, ops[k].text)
			}
		}
		hunks = append(hunks, h)
	}
	return hunks
}

// diffOps computes a line-level edit script via longest-common-subsequence.
// For very large inputs it falls back to a coarse multiset difference so the
// cost stays bounded (panes are normally small, but --lines can widen them).
func diffOps(a, b []string) []op {
	n, m := len(a), len(b)
	if n == 0 {
		ops := make([]op, m)
		for j := range b {
			ops[j] = op{'+', b[j]}
		}
		return ops
	}
	if m == 0 {
		ops := make([]op, n)
		for i := range a {
			ops[i] = op{'-', a[i]}
		}
		return ops
	}
	if n*m > 4_000_000 {
		return coarseOps(a, b)
	}

	// dp[i][j] = LCS length of a[i:], b[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{'=', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}

// coarseOps is the bounded fallback: lines present only in a are removals,
// lines present only in b are additions (by multiset). It loses ordering but is
// O(n+m) and never blows up on huge panes.
func coarseOps(a, b []string) []op {
	counts := map[string]int{}
	for _, l := range a {
		counts[l]++
	}
	for _, l := range b {
		counts[l]--
	}
	var ops []op
	for _, l := range a {
		if counts[l] > 0 {
			ops = append(ops, op{'-', l})
			counts[l]--
		}
	}
	for _, l := range b {
		if counts[l] < 0 {
			ops = append(ops, op{'+', l})
			counts[l]++
		}
	}
	return ops
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func tail(s string, n int) []string {
	ls := splitLines(s)
	if ls == nil {
		return []string{}
	}
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return ls
}
