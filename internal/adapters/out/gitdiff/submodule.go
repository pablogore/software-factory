package gitdiff

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// dirtySubmodules are the submodules of the view v that listed doesn't have
// and whose checkout has files that differ from the commit their gitlink
// keeps, each as one modified submodule. Each is checked with a view of its
// own, as Changes checks the worktree, so none of the submodule's config,
// hooks or attributes run. A submodule inside one is checked the same way.
func dirtySubmodules(ctx context.Context, v view, listed []domain.FileChange) ([]domain.FileChange, error) {
	out, err := v.git(ctx, "ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, c := range listed {
		seen[c.Path] = true
	}
	var dirty []domain.FileChange
	for _, entry := range nulFields(out) {
		meta, p, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("gitdiff: unexpected git ls-files output %q: %w", entry, domain.ErrInvalidValue)
		}
		if f[0] != gitlink || seen[p] {
			continue
		}
		changed, err := submoduleDirty(ctx, v, p, f[1])
		if err != nil {
			return nil, err
		}
		if changed {
			dirty = append(dirty, domain.FileChange{Path: p, Kind: domain.ChangeModified, Repo: domain.RepoSubmodule})
		}
	}
	return dirty, nil
}

// submoduleDirty reports whether the submodule at p, from the top of the
// view v, has files that differ from commit, as Changes would list them.
// One that isn't checked out, with no .git in its folder, has none, as git
// sees it.
func submoduleDirty(ctx context.Context, v view, p, commit string) (bool, error) {
	dir := filepath.Join(v.top, filepath.FromSlash(p))
	if _, err := os.Lstat(filepath.Join(dir, ".git")); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("gitdiff: %w", err)
	}
	// A symlink on the way would have the submodule's view read a folder
	// outside the worktree, and could loop back into it for good; git diff
	// lists such a path already, so one here changed.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false, fmt.Errorf("gitdiff: %w", err)
	}
	if resolved != dir {
		return true, nil
	}
	sub, err := newView(ctx, v.home, dir)
	if err != nil {
		return false, err
	}
	defer sub.close()
	changes, err := sub.changes(ctx, dir, commit)
	return len(changes) > 0, err
}
