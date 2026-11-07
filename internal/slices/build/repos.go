package build

import (
	"github.com/pablogore/software-factory/internal/domain"
)

// ReposChanged is one reason for each repo of its own that changes have,
// naming it, in the order of changes: the gate doesn't look inside one, so
// its files would skip every other rule. A nested repo always breaks the
// rule, since a builder has no reason to make one, whatever the base
// ignores. A submodule added, modified or deleted breaks it unless the
// AllowedPaths of one of roles cover it; a role with none allows no
// submodule.
func ReposChanged(changes []domain.FileChange, roles []domain.Role) []string {
	var allowed []string
	for _, r := range roles {
		allowed = append(allowed, r.AllowedPaths...)
	}
	var reasons []string
	for _, c := range changes {
		switch c.Repo {
		case domain.RepoNested:
			reasons = append(reasons, "the worktree has "+c.Path+", a folder with its own .git that hides its files from the gate")
		case domain.RepoSubmodule:
			if matches(allowed, c.Path) {
				continue
			}
			verb := "modifies"
			switch c.Kind {
			case domain.ChangeAdded:
				verb = "adds"
			case domain.ChangeDeleted:
				verb = "deletes"
			}
			reasons = append(reasons, "the diff "+verb+" the submodule "+c.Path+", which no build role's allowed paths cover")
		}
	}
	return reasons
}
