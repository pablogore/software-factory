package build

import (
	"slices"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// Roles are the roles of p's build stage, as loaded with p. A role with
// no role file, such as the default builder, is not among them.
func Roles(p domain.Pipeline) []domain.Role {
	var out []domain.Role
	for _, st := range p.Stages {
		if st.Kind != domain.KindBuild {
			continue
		}
		for _, r := range p.Roles {
			if slices.Contains(st.Roles, r.Name) {
				out = append(out, r)
			}
		}
	}
	return out
}

// OutsideAllowed is one reason for each file changes add, modify or delete
// outside what roles declare in AllowedPaths, naming the file, in the order of
// changes. A file is allowed when any role allows it. When no role declares
// allowed paths, or one of them declares none and so may touch anything,
// nothing is outside.
func OutsideAllowed(changes []domain.FileChange, roles []domain.Role) []string {
	if len(roles) == 0 {
		return nil
	}
	var allowed []string
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		if len(r.AllowedPaths) == 0 {
			return nil
		}
		allowed = append(allowed, r.AllowedPaths...)
		names = append(names, r.Name)
	}
	var reasons []string
	for _, c := range changes {
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
		reasons = append(reasons, "the diff "+verb+" "+c.Path+", outside the allowed paths of build role "+strings.Join(names, ", "))
	}
	return reasons
}
