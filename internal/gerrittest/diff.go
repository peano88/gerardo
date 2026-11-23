package gerrittest

import (
	"strings"

	"github.com/peano88/gerardo/internal/gerrit"
)

// splitLines splits a file into its lines, without a trailing empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type opKind int

const (
	opCommon opKind = iota
	opDelete
	opInsert
)

// diffLines returns the edit script turning a into b, from a longest common
// subsequence of their lines.
func diffLines(a, b []string) []opKind {
	n, m := len(a), len(b)
	// lcs[i*(m+1)+j] is the length of the LCS of a[i:] and b[j:].
	lcs := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*(m+1)+j] = lcs[(i+1)*(m+1)+j+1] + 1
			} else {
				lcs[i*(m+1)+j] = max(lcs[(i+1)*(m+1)+j], lcs[i*(m+1)+j+1])
			}
		}
	}
	var ops []opKind
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, opCommon)
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i*(m+1)+j+1] >= lcs[(i+1)*(m+1)+j]):
			ops = append(ops, opInsert)
			j++
		default:
			ops = append(ops, opDelete)
			i++
		}
	}
	return ops
}

// diffContent groups the lines of a and b into the chunks of a Gerrit diff.
func diffContent(a, b []string) []gerrit.DiffContent {
	var out []gerrit.DiffContent
	i, j := 0, 0
	ops := diffLines(a, b)
	for k := 0; k < len(ops); {
		var c gerrit.DiffContent
		if ops[k] == opCommon {
			for ; k < len(ops) && ops[k] == opCommon; k++ {
				c.AB = append(c.AB, a[i])
				i, j = i+1, j+1
			}
		} else {
			for ; k < len(ops) && ops[k] != opCommon; k++ {
				if ops[k] == opDelete {
					c.A = append(c.A, a[i])
					i++
				} else {
					c.B = append(c.B, b[j])
					j++
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// countChanges returns the lines inserted and deleted from a to b.
func countChanges(a, b []string) (inserted, deleted int) {
	for _, op := range diffLines(a, b) {
		switch op {
		case opInsert:
			inserted++
		case opDelete:
			deleted++
		}
	}
	return inserted, deleted
}

// mapLine returns the line of b matching line (1-based) of a: the same line
// when it is kept, else the line following the edit that replaced it.
func mapLine(a, b []string, line int) int {
	i, j := 0, 0
	for _, op := range diffLines(a, b) {
		switch op {
		case opCommon:
			i, j = i+1, j+1
			if i == line {
				return j
			}
		case opDelete:
			i++
			if i == line {
				return min(j+1, max(len(b), 1))
			}
		case opInsert:
			j++
		}
	}
	return min(line, max(len(b), 1))
}
