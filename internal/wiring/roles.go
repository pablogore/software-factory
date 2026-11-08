package wiring

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

// ErrUnknownRole means no role file has the name asked for.
var ErrUnknownRole = errors.New("role not configured")

// RoleSession is what a role's session starts with, read from the
// configuration when the session is launched.
type RoleSession struct {
	Role domain.Role
	// Prompt is the text of the role's prompt file.
	Prompt string
	// Agent launches the agent the role names, with the role's model.
	Agent acp.Config
}

// LoadRoleSession reads role name from dir, a configuration root:
// roles/<name>.yaml, the prompt file it names and roles.yaml's agents. The
// files are read on every call, so a changed agent or model reaches the next
// session without rebuilding factoryd.
func LoadRoleSession(dir, name string) (RoleSession, error) {
	// Checked before it becomes a path, so a name like ../x can't reach a
	// file outside roles/.
	if err := domain.ValidateRoleName(name); err != nil {
		return RoleSession{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "roles", name+".yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return RoleSession{}, fmt.Errorf("role %s: %w: no roles/%s.yaml in %s", name, ErrUnknownRole, name, dir)
	}
	if err != nil {
		return RoleSession{}, fmt.Errorf("role %s: %w", name, err)
	}
	r, err := configyaml.LoadRole(data)
	if err != nil {
		return RoleSession{}, err
	}
	if r.Name != name {
		return RoleSession{}, fmt.Errorf("roles/%s.yaml: %w: name %q", name, domain.ErrInvalidValue, r.Name)
	}
	if r.Prompt == "" {
		return RoleSession{}, fmt.Errorf("role %s: %w: prompt", name, domain.ErrMissingField)
	}
	prompt, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(r.Prompt)))
	if err != nil {
		return RoleSession{}, fmt.Errorf("role %s: prompt: %w", name, err)
	}
	if strings.TrimSpace(string(prompt)) == "" {
		return RoleSession{}, fmt.Errorf("role %s: %w: prompt %s is empty", name, domain.ErrInvalidValue, r.Prompt)
	}
	data, err = os.ReadFile(filepath.Join(dir, "roles.yaml"))
	if err != nil {
		return RoleSession{}, fmt.Errorf("role %s: %w", name, err)
	}
	agents, err := configyaml.LoadAgents(data)
	if err != nil {
		return RoleSession{}, err
	}
	a, err := agents.For(r)
	if err != nil {
		return RoleSession{}, err
	}
	return RoleSession{Role: r, Prompt: string(prompt), Agent: acp.Config{Command: a.Command, Args: a.Args, Env: a.Env}}, nil
}
