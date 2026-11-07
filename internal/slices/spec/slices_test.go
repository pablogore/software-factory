package spec_test

import (
	"fmt"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/slices/spec"
)

// The slices.md cases of factory/tests/test_specgate.py on main, plus the
// type and repeated-ID checks its specgate has but doesn't test.

const table = "| ID | Tipo | Valor | Estado |\n|----|------|-------|--------|\n| S1 | slice | a | %s |\n| S2 | %s | b | %s |\n"

func sliceTable(first, second string) string { return fmt.Sprintf(table, first, "spike", second) }

type slicesRow struct {
	name   string
	text   string
	done   bool
	ok     bool
	needle string
}

func slicesRows() []slicesRow {
	return []slicesRow{
		{"one next passes", sliceTable("next", "idea"), false, true, "SPECGATE: PASS"},
		{"two nexts fail", sliceTable("next", "next"), false, false, "found 2"},
		{"no next fails", sliceTable("idea", "idea"), false, false, "found 0"},
		{"a done initiative without next passes", sliceTable("merged", "done"), true, true, "SPECGATE: PASS"},
		{"a done initiative with a next fails", sliceTable("merged", "next"), true, false, "DONE but"},
		{"a STATUS line fails", "STATUS: CONTINUE\n" + sliceTable("next", "idea"), false, false, "must not have a STATUS"},
		{"an unknown state fails", sliceTable("next", "wip"), false, false, "state 'wip'"},
		{"an unknown type fails naming the row", fmt.Sprintf(table, "next", "story", "idea"), false, false, "S2: type 'story'"},
		{"a repeated ID fails naming it", sliceTable("next", "idea") + "| S1 | slice | c | idea |\n", false, false, "slice S1 appears more than once"},
		{"a table without rows fails", "| ID | Tipo | Valor | Estado |\n|----|------|-------|--------|\n", false, false, "no slice rows found"},
	}
}

func TestSpecgateSlices(t *testing.T) {
	specs.Describe(t, "specgate on a slices.md", func(s *specs.Spec) {
		specs.Table(s, slicesRows(), func(r slicesRow) string { return r.name }, func(ctx *specs.Context, r slicesRow) {
			rep := spec.CheckSlices(r.text, r.done)
			// the report first, so a failing case shows specgate's errors
			ctx.Expect(rep.String()).To(specs.Contain(r.needle))
			ctx.Expect(rep.Verdict == spec.VerdictPass).ToEqual(r.ok)
		})

		s.It("prints the errors as main does", func(ctx *specs.Context) {
			rep := spec.CheckSlices(sliceTable("next", "wip"), false)
			ctx.Expect(rep.String()).ToEqual("SPECGATE: FAIL\n" +
				"- S2: state 'wip' isn't one of done, dropped, idea, in-progress, merged, next\n")
			rep = spec.CheckSlices(fmt.Sprintf(table, "next", "story", "idea")+"| S1 | slice | c | idea |\n", false)
			ctx.Expect(rep.String()).ToEqual("SPECGATE: FAIL\n" +
				"- slice S1 appears more than once\n" +
				"- S2: type 'story' isn't one of slice, spike\n")
		})
	})
}
