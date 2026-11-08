package wiring

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrNoRoles means a configuration root has no role files at all.
var ErrNoRoles = errors.New("no roles configured")

// ValidateRoles loads every role in dir's roles/ as a session would, so a
// role that fails to load fails here with the same error. What loads but is
// worth a look comes back as warnings: a reviewer on the builder's agent and
// model reviews with the same blind spots it built with.
func ValidateRoles(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "roles", "*.yaml"))
	if err != nil {
		return nil, err
	}
	// A mistyped root has no roles to fail on, so it would pass as valid.
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: no roles/*.yaml in %s", ErrNoRoles, dir)
	}
	sort.Strings(files)
	var roles []domain.Role
	for _, f := range files {
		rs, err := LoadRoleSession(dir, strings.TrimSuffix(filepath.Base(f), ".yaml"))
		if err != nil {
			return nil, err
		}
		roles = append(roles, rs.Role)
	}
	var warnings []string
	for _, b := range roles {
		if b.Kind != domain.KindBuild {
			continue
		}
		for _, r := range roles {
			if r.Kind == domain.KindReview && r.Agent == b.Agent && r.Model == b.Model {
				warnings = append(warnings, fmt.Sprintf("%s uses the same agent and model as %s: %s %s", r.Name, b.Name, r.Agent, r.Model))
			}
		}
	}
	return warnings, nil
}
