package domain

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

// DiffSummary is what a job's diff against its base changed, as a role's
// When reads it: the paths of the files, from the repo root, and the lines
// added plus deleted.
type DiffSummary struct {
	Paths []string `json:"paths,omitempty"`
	Lines int      `json:"lines"`
}

// Rating is intake's rating of a job, as a role's When reads it. An empty
// Risk means intake never rated the job, as on jobs accepted before it did.
type Rating struct {
	Risk     Risk
	Surfaces []string
}

// Applies reports whether the role w belongs to runs on diff and rating
// and, when it doesn't, why. A When with no conditions always applies. A
// condition the job can't evaluate yet applies, so a role is never skipped
// for want of data: labels and deps_changed, paths and diff_lines when diff
// is nil, and surfaces when intake never rated the job. An unrated job's
// risk counts as high, so a role kept for risky work runs.
func (w When) Applies(diff *DiffSummary, rating Rating) (bool, string) {
	var why []string
	for _, c := range w.All {
		if ok, reason := c.holds(diff, rating); !ok {
			why = append(why, reason)
		}
	}
	if len(why) > 0 {
		return false, "when does not apply: " + strings.Join(why, "; ")
	}
	for _, c := range w.Any {
		ok, reason := c.holds(diff, rating)
		if ok {
			return true, ""
		}
		why = append(why, reason)
	}
	if len(why) > 0 {
		return false, "when does not apply: " + strings.Join(why, "; ")
	}
	return true, ""
}

// holds reports whether every key c sets holds on diff and rating and,
// when one doesn't, why.
func (c Condition) holds(diff *DiffSummary, rating Rating) (bool, string) {
	risk := rating.Risk
	if risk == "" {
		risk = RiskHigh
	}
	if len(c.Risk) > 0 && !slices.Contains(c.Risk, risk) {
		return false, fmt.Sprintf("the intake risk is %s, not %s", risk, joinRisks(c.Risk))
	}
	if len(c.Surfaces) > 0 && rating.Risk != "" && !slices.ContainsFunc(c.Surfaces, func(s string) bool { return slices.Contains(rating.Surfaces, s) }) {
		return false, fmt.Sprintf("no intake surface matches surfaces %s", strings.Join(c.Surfaces, ", "))
	}
	if diff == nil || len(c.Labels) > 0 || c.DepsChanged {
		return true, ""
	}
	if len(c.Paths) > 0 && !matchesAny(c.Paths, diff.Paths) {
		return false, fmt.Sprintf("no changed file matches paths %s", strings.Join(c.Paths, ", "))
	}
	if c.DiffLines != "" {
		op, n, err := parseDiffLines(c.DiffLines)
		if err != nil {
			return true, ""
		}
		if !compare(diff.Lines, op, n) {
			return false, fmt.Sprintf("the diff has %d lines, not %s", diff.Lines, c.DiffLines)
		}
	}
	return true, ""
}

// validate checks that every path pattern, risk level, surface and the
// diff_lines expression of c are well formed.
func (c Condition) validate() error {
	for _, r := range c.Risk {
		if r != RiskLow && r != RiskMedium && r != RiskHigh {
			return fmt.Errorf("%w: when risk %q, want low, medium or high", ErrInvalidValue, r)
		}
	}
	for _, s := range c.Surfaces {
		if Blank(s) {
			return fmt.Errorf("%w: when surface %q is blank", ErrInvalidValue, s)
		}
	}
	for _, p := range c.Paths {
		if _, err := path.Match(strings.ReplaceAll(p, "**", "*"), ""); p == "" || err != nil {
			return fmt.Errorf("%w: when path %q", ErrInvalidValue, p)
		}
	}
	if c.DiffLines != "" {
		if _, _, err := parseDiffLines(c.DiffLines); err != nil {
			return err
		}
	}
	return nil
}

func joinRisks(rs []Risk) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = string(r)
	}
	return strings.Join(names, ", ")
}

// matchesAny reports whether one of paths matches one of the patterns.
func matchesAny(patterns, paths []string) bool {
	for _, p := range paths {
		for _, pat := range patterns {
			if MatchGlob(pat, p) {
				return true
			}
		}
	}
	return false
}

// MatchGlob reports whether name, a slash-separated path, matches pattern:
// each segment as path.Match takes it, and a "**" segment for any number of
// segments, none included.
func MatchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegments(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	return err == nil && ok && matchSegments(pat[1:], name[1:])
}

// diffOps are the comparisons diff_lines takes, longest first so ">=" is
// not read as ">".
var diffOps = []string{">=", "<=", "==", ">", "<"}

// parseDiffLines reads a diff_lines expression such as ">= 200".
func parseDiffLines(expr string) (string, int, error) {
	s := strings.TrimSpace(expr)
	for _, op := range diffOps {
		if rest, ok := strings.CutPrefix(s, op); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || n < 0 {
				break
			}
			return op, n, nil
		}
	}
	return "", 0, fmt.Errorf("%w: diff_lines %q, want an operator (>=, <=, ==, >, <) and a number", ErrInvalidValue, expr)
}

func compare(lines int, op string, n int) bool {
	switch op {
	case ">=":
		return lines >= n
	case "<=":
		return lines <= n
	case "==":
		return lines == n
	case ">":
		return lines > n
	default:
		return lines < n
	}
}
