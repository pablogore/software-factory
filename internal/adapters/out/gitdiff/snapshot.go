package gitdiff

import (
	"context"
	"crypto/sha1" //nolint:gosec // git names its SHA-1 objects with it.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/pablogore/software-factory/internal/domain"
)

// snapshot writes the worktree at dir to a scratch index, as git would stage
// every tracked file, and returns that index's path in a scratch folder for
// the caller to remove. git reads no file of the worktree for it: factoryd
// does, so no clean or process filter the worktree's .gitattributes,
// info/attributes or core.attributesFile name ever runs, and a symlink is
// kept as its target, never followed out of the worktree. Untracked files
// are left out, as git diff leaves them; a submodule and a file the index
// marks skip-worktree or assume-unchanged keep their index entry, as git
// diff keeps them.
func snapshot(ctx context.Context, dir string) (string, string, error) {
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	format, err := git(ctx, dir, "rev-parse", "--show-object-format")
	if err != nil {
		return "", "", err
	}
	newHash := sha1.New
	if strings.TrimSpace(format) == "sha256" {
		newHash = sha256.New
	}
	flagged, err := Differ{}.Flagged(ctx, dir)
	if err != nil {
		return "", "", err
	}
	keep := map[string]bool{}
	for _, f := range flagged {
		keep[f.Path] = true
	}
	out, err := git(ctx, dir, "ls-files", "--stage", "--full-name", "-z", "--", ":/")
	if err != nil {
		return "", "", err
	}
	root, err := os.OpenRoot(strings.TrimSpace(top))
	if err != nil {
		return "", "", fmt.Errorf("gitdiff: %w", err)
	}
	defer func() { _ = root.Close() }()
	var entries strings.Builder
	seen := map[string]bool{}
	for _, entry := range nulFields(out) {
		meta, p, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return "", "", fmt.Errorf("gitdiff: unexpected git ls-files output %q: %w", entry, domain.ErrInvalidValue)
		}
		// An unmerged file has one entry per stage; the worktree has one copy.
		if seen[p] {
			continue
		}
		seen[p] = true
		mode, object := f[0], f[1]
		if mode != gitlink && !keep[p] {
			var found bool
			mode, object, found, err = staged(ctx, dir, root, p, newHash, object, f[2] == "0")
			if err != nil {
				return "", "", err
			}
			if !found {
				continue
			}
		}
		entries.WriteString(mode + " " + object + "\t" + p + "\x00")
	}
	scratch, err := os.MkdirTemp("", "gitdiff-index-")
	if err != nil {
		return "", "", fmt.Errorf("gitdiff: %w", err)
	}
	index := filepath.Join(scratch, "index")
	// A split index or an untracked cache, which the shared config can turn
	// on, would write files next to the real index.
	if _, err := runEnv(ctx, dir, entries.String(), []string{"GIT_INDEX_FILE=" + index},
		"-c", "core.splitIndex=false", "-c", "core.untrackedCache=false", "update-index", "-z", "--index-info"); err != nil {
		_ = os.RemoveAll(scratch)
		return "", "", err
	}
	return scratch, index, nil
}

// staged is the mode and object name git would stage p, a tracked file by
// its path from the root, with, given the index's object, which stands when
// the content is the same and the entry is clean, not unmerged; false when the
// worktree has no file or symlink at p, as for a file deleted or replaced by
// a folder. root is the worktree, so nothing outside it is read.
func staged(ctx context.Context, dir string, root *os.Root, p string, newHash func() hash.Hash, object string, clean bool) (string, string, bool, error) {
	// git sees no file under a parent that is a symlink or not a folder.
	for d := path.Dir(p); d != "."; d = path.Dir(d) {
		info, err := root.Lstat(d)
		if missing(err) || (err == nil && !info.IsDir()) {
			return "", "", false, nil
		}
		if err != nil {
			return "", "", false, fmt.Errorf("gitdiff: %w", err)
		}
	}
	info, err := root.Lstat(p)
	if missing(err) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("gitdiff: %w", err)
	}
	var mode string
	var content []byte
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := root.Readlink(p)
		if err != nil {
			return "", "", false, fmt.Errorf("gitdiff: %w", err)
		}
		mode, content = "120000", []byte(target)
	case info.Mode().IsRegular():
		// Root opens a file swapped for a symlink since Lstat only if it
		// points inside the worktree.
		content, err = root.ReadFile(p)
		if err != nil {
			return "", "", false, fmt.Errorf("gitdiff: %w", err)
		}
		mode = "100644"
		if info.Mode()&0o111 != 0 {
			mode = "100755"
		}
	default:
		return "", "", false, nil
	}
	h := newHash()
	h.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	h.Write(content)
	if name := hex.EncodeToString(h.Sum(nil)); clean && name == object {
		return mode, object, true, nil
	}
	// With --stdin and no --path, git looks up no attributes for the content.
	name, err := run(ctx, dir, string(content), "hash-object", "-w", "--no-filters", "--stdin")
	if err != nil {
		return "", "", false, err
	}
	return mode, strings.TrimSpace(name), true, nil
}

// missing reports whether err says there is no file at a path, or a file
// where the path needs a folder.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
