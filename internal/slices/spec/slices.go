package spec

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// The slice table's types and states on main, sorted as its messages list them.
var (
	sliceKinds  = []string{"slice", "spike"}
	sliceStates = []string{"done", "dropped", "idea", "in-progress", "merged", "next"}
)

// sliceRow is the regular expression of specgate on main: ID, type, any
// middle columns, state.
var sliceRow = regexp.MustCompile(`^\|\s*([A-Za-z0-9_.-]+)\s*\|\s*([^|]*?)\s*\|(.*)\|\s*([^|]*?)\s*\|\s*$`)

type slice struct{ id, kind, state string }

// CheckSlices runs specgate on a slices.md. done says the initiative is over
// (replan DONE), so no slice may be `next`; otherwise exactly one must be.
func CheckSlices(text string, done bool) Report {
	if errs := checkSlices(text, done); len(errs) > 0 {
		return Report{Verdict: VerdictFail, Errors: errs}
	}
	return Report{Verdict: VerdictPass}
}

func checkSlices(text string, done bool) []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "STATUS:") {
		add("slices.md must not have a STATUS line (replan.md carries it)")
	}
	rows := sliceRows(text)
	if len(rows) == 0 {
		add("no slice rows found")
	}
	seen := map[string]int{}
	for _, r := range rows {
		seen[r.id]++
	}
	var dups []string
	for id, n := range seen {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	for _, id := range dups {
		add("slice %s appears more than once", id)
	}
	var nexts []string
	for _, r := range rows {
		if !slices.Contains(sliceKinds, r.kind) {
			add("%s: type %s isn't one of %s", r.id, pyRepr(r.kind), strings.Join(sliceKinds, ", "))
		}
		if !slices.Contains(sliceStates, r.state) {
			add("%s: state %s isn't one of %s", r.id, pyRepr(r.state), strings.Join(sliceStates, ", "))
		}
		if r.state == "next" {
			nexts = append(nexts, r.id)
		}
	}
	switch {
	case done && len(nexts) > 0:
		add("DONE but slices still `next`: %s", strings.Join(nexts, ", "))
	case !done && len(nexts) != 1:
		add("exactly one `next` is required, found %d", len(nexts))
	}
	return errs
}

// sliceRows are the table's rows, without its header and separator.
func sliceRows(text string) []slice {
	var rows []slice
	for _, l := range strings.Split(text, "\n") {
		m := sliceRow.FindStringSubmatch(strings.TrimSuffix(l, "\r"))
		if m == nil || m[1] == "ID" || strings.Trim(m[1], "-") == "" {
			continue
		}
		rows = append(rows, slice{id: m[1], kind: m[2], state: m[4]})
	}
	return rows
}
