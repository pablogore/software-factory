package build

import (
	"path"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// ProtectedPaths are what the gate and the rules a builder is judged by are
// made of, so a job may not modify or delete them; adding a new file under
// one is allowed, except under ProtectedAdds. An entry ending in "/" covers
// everything under it, any other is a path.Match pattern from the repo root.
var ProtectedPaths = []string{
	"Makefile",
	"GNUmakefile",
	"makefile",
	"internal/arch_test.go",
	".github/",
	"AGENTS.md",
	".golangci.yml",
	"docs/factoryd/0*.md",
	// The repo's gate config: the gate reads it at the base, but a merged
	// change would loosen the gate of every job after it.
	".factory/",
}

// ProtectedAdds are the protected paths a job may not add to either: make
// reads a GNUmakefile or a makefile before the Makefile, and any new file
// under .github/ can be a workflow, an action or a CODEOWNERS that GitHub
// runs or enforces. A new file under .factory/ could be the repo's gate
// config, which the next jobs' gates read once it is merged.
var ProtectedAdds = []string{
	"GNUmakefile",
	"makefile",
	".github/",
	".factory/",
}

// ProtectedTouched is one reason for each protected file that changes
// modifies or deletes, or adds under ProtectedAdds, naming the file, in the
// order of changes.
func ProtectedTouched(changes []domain.FileChange) []string {
	var reasons []string
	for _, c := range changes {
		if !matches(ProtectedPaths, c.Path) {
			continue
		}
		verb := "modifies"
		switch c.Kind {
		case domain.ChangeAdded:
			if !matches(ProtectedAdds, c.Path) {
				continue
			}
			verb = "adds"
		case domain.ChangeDeleted:
			verb = "deletes"
		}
		reasons = append(reasons, "the diff "+verb+" the protected path "+c.Path)
	}
	return reasons
}

// matches reports whether file is covered by one of patterns: an entry
// ending in "/" covers everything under it, any other is a path.Match
// pattern from the repo root.
func matches(patterns []string, file string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(file, p) {
				return true
			}
			continue
		}
		if ok, _ := path.Match(p, file); ok {
			return true
		}
	}
	return false
}
