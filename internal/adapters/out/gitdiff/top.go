package gitdiff

import (
	"fmt"
	"path/filepath"

	"github.com/pablogore/software-factory/internal/domain"
)

// checkTop is top, the toplevel git gave for the worktree at dir, with no
// symlink in it, once it is dir itself. core.worktree, in the config, in
// config.worktree or in the git folder a rewritten .git points at, is the
// builder's to set, and moves the toplevel anywhere, even to a folder above
// the worktree; so does a deleted .git, since git then finds the repo the
// worktree folder sits in. git would then read the files there as the
// worktree's, so it fails instead.
func checkTop(dir, top string) (string, error) {
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	if want, err = filepath.Abs(want); err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil || realTop != want {
		return "", fmt.Errorf("gitdiff: git puts the toplevel of the worktree at %s, not at %s: core.worktree or a rewritten or missing .git moved it: %w", top, dir, domain.ErrInvalidValue)
	}
	return realTop, nil
}
