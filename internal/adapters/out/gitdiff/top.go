package gitdiff

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pablogore/software-factory/internal/domain"
)

// checkTop is top, the toplevel git gave for the worktree at dir, with no
// symlink in it, once it is the folder nearest dir with a .git in it: where
// git puts the toplevel when no config moves it. core.worktree, in the
// config, in config.worktree or in the git folder a rewritten .git points
// at, is the builder's to set, and moves the toplevel anywhere, even to a
// folder above the worktree: git would then read the files there as the
// worktree's, so it fails instead.
func checkTop(dir, top string) (string, error) {
	want, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	for {
		_, err := os.Lstat(filepath.Join(want, ".git"))
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("gitdiff: %w", err)
		}
		up := filepath.Dir(want)
		if up == want {
			return "", fmt.Errorf("gitdiff: %s has no .git in it or above it: %w", dir, domain.ErrInvalidValue)
		}
		want = up
	}
	realWant, err := filepath.EvalSymlinks(want)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil || realTop != realWant {
		return "", fmt.Errorf("gitdiff: git puts the toplevel of the worktree at %s, not at %s: core.worktree or a rewritten .git moved it: %w", top, want, domain.ErrInvalidValue)
	}
	return realTop, nil
}
