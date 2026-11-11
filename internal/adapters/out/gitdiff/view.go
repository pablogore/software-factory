package gitdiff

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// view is a repo of its own, in a scratch folder, that sees the worktree's
// files and objects but none of its config, hooks, attributes or index.
// git diff refreshes the index it reads, and with the worktree's own that
// runs the hooks and the clean and process filters its config names, writes
// the index back, and trusts the stat data of each entry, which the builder
// can fake to hide a change. The view's index is made fresh from a copy of
// the real one, which is only ever read, and has no stat data, so git hashes
// every tracked file again; its attributes come from the empty tree, so a
// text or eol line in the worktree's .gitattributes can't normalise a change
// back to the base.
type view struct {
	home, top, scratch string
	env                []string
}

// newView makes the view of the worktree at dir, with git run with home as
// its HOME. close removes it. A split index fails here: its shared part is in
// the worktree's git folder, which the view doesn't read, so the copy can't
// be read on its own and nothing can hide behind it.
func newView(ctx context.Context, home, dir string) (view, error) {
	// The index comes before --path-format=absolute, which resolves a symlink
	// at its name, and copyIndex must see the symlink to refuse it.
	out, err := git(ctx, home, dir, "rev-parse", "--git-path", "index", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--show-object-format")
	if err != nil {
		return view{}, err
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		return view{}, fmt.Errorf("gitdiff: unexpected git rev-parse output %q: %w", out, domain.ErrInvalidValue)
	}
	index, top, common, format := lines[0], lines[1], lines[2], lines[3]
	if !filepath.IsAbs(index) {
		index = filepath.Join(dir, index)
	}
	scratch, err := os.MkdirTemp("", "gitdiff-view-")
	if err != nil {
		return view{}, fmt.Errorf("gitdiff: %w", err)
	}
	v := view{home: home, top: top, scratch: scratch}
	if err := v.fill(ctx, common, index, format); err != nil {
		v.close()
		return view{}, err
	}
	return v, nil
}

func (v *view) fill(ctx context.Context, common, index, format string) error {
	gitDir := filepath.Join(v.scratch, "g")
	// No template: one could bring hooks, an exclude file or a config.
	if _, err := git(ctx, v.home, v.scratch, "init", "-q", "--bare", "--template=", "--object-format="+format, gitDir); err != nil {
		return err
	}
	info := filepath.Join(gitDir, "objects", "info")
	if err := os.MkdirAll(info, 0o700); err != nil {
		return fmt.Errorf("gitdiff: %w", err)
	}
	if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(filepath.Join(common, "objects")+"\n"), 0o600); err != nil {
		return fmt.Errorf("gitdiff: %w", err)
	}
	copied := filepath.Join(v.scratch, "index.copy")
	if err := copyIndex(index, copied); err != nil {
		return err
	}
	v.env = []string{"GIT_DIR=" + gitDir, "GIT_COMMON_DIR=" + gitDir, "GIT_WORK_TREE=" + v.top}
	empty, err := run(ctx, v.home, v.scratch, "", v.env, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return err
	}
	v.env = append(v.env, "GIT_ATTR_SOURCE="+strings.TrimSpace(empty))
	entries, err := run(ctx, v.home, v.top, "", append(v.env, "GIT_INDEX_FILE="+copied), "ls-files", "-s", "-z")
	if err != nil {
		return err
	}
	v.env = append(v.env, "GIT_INDEX_FILE="+filepath.Join(v.scratch, "index"))
	_, err = run(ctx, v.home, v.top, entries, v.env, "update-index", "-z", "--index-info")
	return err
}

// maxIndex bounds the index copyIndex copies, far above any real one.
const maxIndex = 512 << 20

// copyIndex copies the real index at from to to. The builder picks what is
// at from, and the copy runs in factoryd, where ctx can't stop it: a FIFO
// there would block it for good and /dev/zero would fill its memory, so it
// takes only a regular file, not through a symlink, of at most maxIndex.
func copyIndex(from, to string) error {
	src, err := openIndex(from)
	if err != nil {
		return fmt.Errorf("gitdiff: opening the index: %w", err)
	}
	defer func() { _ = src.Close() }()
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("gitdiff: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("gitdiff: the index %s is not a regular file: %w", from, domain.ErrInvalidValue)
	}
	if info.Size() > maxIndex {
		return fmt.Errorf("gitdiff: the index %s is over %d bytes: %w", from, maxIndex, domain.ErrInvalidValue)
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("gitdiff: %w", err)
	}
	// One byte over the cap tells a file that grew past it since the Stat.
	n, err := io.Copy(dst, io.LimitReader(src, maxIndex+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("gitdiff: copying the index: %w", err)
	}
	if n > maxIndex {
		return fmt.Errorf("gitdiff: the index %s is over %d bytes: %w", from, maxIndex, domain.ErrInvalidValue)
	}
	return nil
}

// git runs git in the view, from the worktree's top.
func (v view) git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, v.home, v.top, "", v.env, args...)
}

func (v view) close() {
	_ = os.RemoveAll(v.scratch)
}
