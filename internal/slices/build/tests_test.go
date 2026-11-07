package build_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/build"
)

// testFileRow is a path and whether its name makes it a test file.
type testFileRow struct {
	path string
	test bool
}

// marksRow is a test file as base had it and as the diff left it, and the
// reasons that fail the gate.
type marksRow struct {
	name    string
	before  string
	after   string
	reasons []string
}

func TestTestRules(t *testing.T) {
	specs.Describe(t, "build.IsTestFile", func(s *specs.Spec) {
		rows := []testFileRow{
			{"internal/slices/build/gate_test.go", true},
			{"x_test.go", true},
			{"ui/app.test.ts", true},
			{"ui/app.spec.tsx", true},
			{"web/test/app.spec.js", true},
			{"internal/slices/build/gate.go", false},
			{"internal/testdata/x.go", false},
			{"test/app.ts", false},
			{"spec.md", false},
			{"my.test/app.go", false},
			{"x_test.go.orig", false},
		}
		specs.Table(s, rows, func(r testFileRow) string { return r.path }, func(ctx *specs.Context, r testFileRow) {
			ctx.Expect(build.IsTestFile(r.path)).ToEqual(r.test)
		})
	})

	specs.Describe(t, "build.TestsDeleted", func(s *specs.Spec) {
		s.It("names each test file deleted, including one moved away, and nothing else", func(ctx *specs.Context) {
			reasons := build.TestsDeleted([]domain.FileChange{
				{Path: "a_test.go", Kind: domain.ChangeDeleted},
				{Path: "b_test.go", Kind: domain.ChangeModified},
				{Path: "c.go", Kind: domain.ChangeDeleted},
				{Path: "ui/old.spec.ts", Kind: domain.ChangeDeleted},
				{Path: "ui/new.spec.ts", Kind: domain.ChangeAdded},
			})

			ctx.Expect(reasons).ToEqual([]string{
				"the diff deletes the test file a_test.go",
				"the diff deletes the test file ui/old.spec.ts",
			})
		})
	})

	specs.Describe(t, "build.MarksAdded", func(s *specs.Spec) {
		rows := []marksRow{
			{"t.Skip added", "func TestA(t *testing.T) {}", "func TestA(t *testing.T) { t.Skip() }", []string{"the diff adds t.Skip to the test file f"}},
			{"t.Skipf added", "", "t.Skipf(\"x %d\", 1)", []string{"the diff adds t.Skip to the test file f"}},
			{"t.SkipNow added", "", "t.SkipNow()", []string{"the diff adds t.Skip to the test file f"}},
			{"it.skip added", "it('a')", "it.skip('a')", []string{"the diff adds .skip to the test file f"}},
			{"describe.only added", "", "describe.only('a', fn)", []string{"the diff adds .only to the test file f"}},
			{"all three added", "", "t.Skip()\nit.skip('a')\nit.only('b')", []string{
				"the diff adds .skip to the test file f",
				"the diff adds .only to the test file f",
				"the diff adds t.Skip to the test file f",
			}},
			{"a second t.Skip added", "t.Skip()", "t.Skip()\nt.Skip()", []string{"the diff adds t.Skip to the test file f"}},
			{"marks the base had, moved", "t.Skip()\nit.only('b')\nx", "x\nit.only('b')\nt.Skip()", nil},
			{"marks the diff removes", "t.Skip()\nit.skip('a')", "", nil},
			{"words that are not marks", "", "skipped := t.Skipped\nfoo.skipped\nx.onlyIf()\nctx.Skip()", nil},
			{"no marks at all", "", "func TestA(t *testing.T) {}", nil},
		}
		specs.Table(s, rows, func(r marksRow) string { return r.name }, func(ctx *specs.Context, r marksRow) {
			ctx.Expect(build.MarksAdded("f", r.before, r.after)).ToEqual(r.reasons)
		})
	})
}
