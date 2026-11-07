package build

import (
	"path"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// ProtectedPaths are what the gate and the rules a builder is judged by are
// made of, so a job may not modify or delete them; adding a new file under
// one is allowed. An entry ending in "/" covers everything under it, any
// other is a path.Match pattern from the repo root.
var ProtectedPaths = []string{
	"Makefile",
	"internal/arch_test.go",
	".github/workflows/",
	"AGENTS.md",
	".golangci.yml",
	"docs/factoryd/0*.md",
}

// ProtectedTouched is one reason for each protected file that changes
// modifies or deletes, naming the file, in the order of changes.
func ProtectedTouched(changes []domain.FileChange) []string {
	var reasons []string
	for _, c := range changes {
		if c.Kind == domain.ChangeAdded || !protected(c.Path) {
			continue
		}
		verb := "modifies"
		if c.Kind == domain.ChangeDeleted {
			verb = "deletes"
		}
		reasons = append(reasons, "the diff "+verb+" the protected path "+c.Path)
	}
	return reasons
}

func protected(file string) bool {
	for _, p := range ProtectedPaths {
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
