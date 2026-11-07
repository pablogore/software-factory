package build

import (
	"path"
	"regexp"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// testMark is a mark that makes a test runner skip a test, or run only some,
// so checks can pass on tests that never ran.
type testMark struct {
	name string
	re   *regexp.Regexp
}

// testMarks are the marks a diff may not add to a test file. t.Skip also
// covers t.Skipf and t.SkipNow, but not t.Skipped, which only asks.
var testMarks = []testMark{
	{".skip", regexp.MustCompile(`\.skip\b`)},
	{".only", regexp.MustCompile(`\.only\b`)},
	{"t.Skip", regexp.MustCompile(`\bt\.Skip(f|Now)?\(`)},
}

// IsTestFile reports whether the file at p is a test by its name: a Go
// _test.go file, or one whose name has .test. or .spec. in it.
func IsTestFile(p string) bool {
	name := path.Base(p)
	return strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.")
}

// TestsDeleted is one reason for each test file changes delete, naming the
// file, in the order of changes. A rename comes as a deletion, so moving a
// test counts as deleting it.
func TestsDeleted(changes []domain.FileChange) []string {
	var reasons []string
	for _, c := range changes {
		if c.Kind == domain.ChangeDeleted && IsTestFile(c.Path) {
			reasons = append(reasons, "the diff deletes the test file "+c.Path)
		}
	}
	return reasons
}

// MarksAdded is one reason for each test mark the test file at p has more
// of in after than in before, naming the file and the mark. Counting rather
// than reading added lines keeps marks the base already had, or that only
// moved, from failing the gate.
func MarksAdded(p, before, after string) []string {
	var reasons []string
	for _, m := range testMarks {
		if len(m.re.FindAllStringIndex(after, -1)) > len(m.re.FindAllStringIndex(before, -1)) {
			reasons = append(reasons, "the diff adds "+m.name+" to the test file "+p)
		}
	}
	return reasons
}
