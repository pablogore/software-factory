package wiring

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

// ErrNoRoles means a configuration root has no role files at all.
var ErrNoRoles = errors.New("no roles configured")

// ValidateRoles loads every role in dir's roles/ as a session would, so a
// role that fails to load fails here with the same error, and then every
// pipeline in dir's pipelines/ against those roles, so a pipeline naming a role
// with no file fails before a job reaches that stage. What loads but is worth
// a look comes back as warnings: a reviewer on the builder's agent and model
// reviews with the same blind spots it built with, and an agent that gets
// ANTHROPIC_API_KEY bills by API, not by the operator's subscription.
func ValidateRoles(dir string) ([]string, error) {
	roles, err := loadRoles(dir)
	if err != nil {
		return nil, err
	}
	if _, err := loadPipelines(dir, roles); err != nil {
		return nil, err
	}
	warnings, err := apiBilled(dir)
	if err != nil {
		return nil, err
	}
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

// apiKey is the variable that has an agent on Anthropic's models bill by
// API instead of by the operator's subscription.
const apiKey = "ANTHROPIC_API_KEY"

// apiBilled is a warning for each agent in dir's roles.yaml that gets
// apiKey, passed from factoryd's environment or set in its env.
func apiBilled(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "roles.yaml"))
	if err != nil {
		return nil, err
	}
	agents, err := configyaml.LoadAgents(data)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, name := range slices.Sorted(maps.Keys(agents)) {
		a := agents[name]
		if slices.Contains(a.PassEnv, apiKey) || slices.ContainsFunc(a.Env, func(kv string) bool { return strings.HasPrefix(kv, apiKey+"=") }) {
			warnings = append(warnings, fmt.Sprintf("agent %s gets %s: it will bill by API, not by your subscription", name, apiKey))
		}
	}
	return warnings, nil
}

// loadRoles loads every role in dir's roles/ as a session would.
func loadRoles(dir string) ([]domain.Role, error) {
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
	return roles, nil
}

// loadPipelines loads every pipelines/*.yaml in dir with roles, naming the
// file of the pipeline that fails to load, and returns them in file order.
func loadPipelines(dir string, roles []domain.Role) ([]domain.Pipeline, error) {
	files, err := filepath.Glob(filepath.Join(dir, "pipelines", "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var ps []domain.Pipeline
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("pipelines/%s: %w", filepath.Base(f), err)
		}
		p, err := configyaml.LoadPipeline(data, roles)
		if err != nil {
			return nil, fmt.Errorf("pipelines/%s: %w", filepath.Base(f), err)
		}
		ps = append(ps, p)
	}
	return ps, nil
}
