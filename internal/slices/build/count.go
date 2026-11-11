package build

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// NoCountCommand is the note a gate with no test count command keeps with
// the run: the rule that tests may not go down did not run.
const NoCountCommand = "the test count rule did not run: no test count command is configured"

// NoCountBase is the note a job with no base commit gets instead of a
// count: there is nothing to count against.
const NoCountBase = "the test count rule did not run: no base commit was recorded to count against"

// testName is a line go test -list prints for one test of a package.
var testName = regexp.MustCompile(`^(Test|Benchmark|Fuzz|Example)\w*$`)

// CountTests is the number of tests of each package in output, as go test
// -list prints it: the names of a package's tests, then a line "ok <package>",
// or "? <package> [no test files]" for one with none. Lines go test -json
// prints are read as its events instead: a package counts each test, subtest
// included, that passed, so every It and specs.Table row of go-specs is one,
// and a test skipped or failed is none. Other lines, such as those go prints
// to stderr, are left out. Names with no package line after them, or a JSON
// line that is not an event, are an ErrInvalidValue error: a count cut short
// can't be trusted.
func CountTests(output string) (map[string]int, error) {
	counts := map[string]int{}
	n := 0
	for line := range strings.Lines(output) {
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "{") {
			if err := countEvent(counts, line); err != nil {
				return nil, err
			}
			continue
		}
		if testName.MatchString(line) {
			n++
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && (f[0] == "ok" || f[0] == "?") {
			counts[f[1]] += n
			n = 0
		}
	}
	if n > 0 {
		return nil, fmt.Errorf("test count: %w: %d test names with no package line after them", domain.ErrInvalidValue, n)
	}
	return counts, nil
}

// testEvent is the part of a go test -json event the count reads.
type testEvent struct {
	Action  string
	Package string
	Test    string
}

// countEvent adds the go test -json event in line to counts: a package it
// names is in counts, with one more test when the event is a test passing.
// Build events name an import path rather than a package, and count nothing.
func countEvent(counts map[string]int, line string) error {
	var ev testEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Action == "" {
		return fmt.Errorf("test count: %w: a line that is not a go test -json event: %s", domain.ErrInvalidValue, line)
	}
	if ev.Package == "" {
		return nil
	}
	if _, ok := counts[ev.Package]; !ok {
		counts[ev.Package] = 0
	}
	if ev.Action == "pass" && ev.Test != "" {
		counts[ev.Package]++
	}
	return nil
}

// TestsDropped is one reason for each package, by name, that has fewer tests
// in head than in base, naming both counts. A package head lacks counts as
// none, so deleting a whole package's tests is a drop too, and a drop in one
// package fails the gate even when another gains more.
func TestsDropped(base, head map[string]int) []string {
	var reasons []string
	for _, pkg := range slices.Sorted(maps.Keys(base)) {
		if head[pkg] < base[pkg] {
			reasons = append(reasons, fmt.Sprintf("the package %s has %d tests, %d at the base commit", pkg, head[pkg], base[pkg]))
		}
	}
	return reasons
}

// testCount runs command, the count command, for job in the worktree at dir
// and in a copy of base apart from it, which the builder can't touch, and is
// the reasons the head fails the rule: a package with fewer tests, or a count
// that failed. It runs with the same runner and ctx as the checks, so with
// the same environment and timeout.
func (g *Gate) testCount(ctx context.Context, job domain.JobID, command []string, dir, base string) ([]string, error) {
	head, reason, err := g.count(ctx, job, command, dir, "the job's head")
	if err != nil || reason != "" {
		return nonEmpty(reason), err
	}
	snap, err := g.Snapshots.Checkout(ctx, job, dir, base)
	if err != nil {
		return nil, fmt.Errorf("copy of base %s: %w", base, err)
	}
	before, reason, err := g.count(ctx, job, command, snap, "the base commit "+base)
	if rmErr := g.Snapshots.Remove(ctx, job, dir, snap); err == nil && rmErr != nil {
		err = fmt.Errorf("copy of base %s: %w", base, rmErr)
	}
	if err != nil || reason != "" {
		return nonEmpty(reason), err
	}
	return TestsDropped(before, head), nil
}

// count is the tests of each package the count command finds in dir, or
// the reason it failed there, at where: a non-zero exit, an output the
// runner cut or one it can't read. The error is for a command that could not
// be run at all.
func (g *Gate) count(ctx context.Context, job domain.JobID, command []string, dir, where string) (map[string]int, string, error) {
	run, err := g.Checks.Run(ctx, job, dir, command)
	if err != nil {
		return nil, "", err
	}
	if run.ExitCode != 0 {
		return nil, fmt.Sprintf("the test count command `%s` failed at %s with exit %d: %s", run.Command, where, run.ExitCode, strings.TrimSpace(run.Output)), nil
	}
	// A runner with limits notes an output it cut, which can't be counted
	// whole.
	if len(run.Notes) > 0 {
		return nil, fmt.Sprintf("the test count command `%s` can't be counted at %s: %s", run.Command, where, strings.Join(run.Notes, "; ")), nil
	}
	counts, err := CountTests(run.Output)
	if err != nil {
		return nil, fmt.Sprintf("the test count command `%s` printed output at %s that can't be read: %v", run.Command, where, err), nil
	}
	return counts, "", nil
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
