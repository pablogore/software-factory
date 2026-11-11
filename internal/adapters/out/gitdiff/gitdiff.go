// Package gitdiff lists the files a job's worktree changed against its base,
// and those its index hides changes of, and reads a file as either has it,
// for the gate's diff rules, and checks the base out apart from the worktree
// for the gate to count its tests.
package gitdiff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// Differ reads diffs with git run for a job with childenv.Base and the
// job's HOME in Homes, the one its checks run with: git may run what the
// repo's config, which the builder can write, tells it to, and that must
// never get factoryd's tokens.
type Differ struct {
	Homes childenv.Homes
}

// Changes lists the files that differ between the worktree at dir,
// committed or not, and base, the full object name of the commit its branch
// was made from. base is never a ref: a ref can be moved from the worktree,
// so a diff against one could hide what the job changed. Renames come as a
// deletion and an addition. A submodule, a gitlink on either side, comes
// marked as one, whether its commit or its checkout changed. An untracked
// file that the base's .gitignore files don't ignore comes last as an
// addition: the builder touched it even though no diff shows it. An
// untracked folder with its own .git comes there too, marked as a nested
// repo, even when the base ignores it: git lists it as one entry and never
// what is inside.
func (d Differ) Changes(ctx context.Context, job domain.JobID, dir, base string) ([]domain.FileChange, error) {
	if base == "" {
		return nil, fmt.Errorf("gitdiff: %w: base", domain.ErrMissingField)
	}
	if !objectName(base) {
		return nil, fmt.Errorf("gitdiff: base %q is not a full commit hash: %w", base, domain.ErrInvalidValue)
	}
	home, err := d.home(job)
	if err != nil {
		return nil, err
	}
	v, err := newView(ctx, home, dir)
	if err != nil {
		return nil, err
	}
	defer v.close()
	return v.changes(ctx, dir, base)
}

// changes is Changes as the view v sees the worktree at dir.
func (v view) changes(ctx context.Context, dir, base string) ([]domain.FileChange, error) {
	// ^{commit} makes git refuse an object that is not a commit, or not there.
	// --raw gives each side's mode, which tells a gitlink. .gitmodules, which
	// the builder can write, may set submodules to be ignored;
	// --ignore-submodules=dirty overrides it and still tells a submodule
	// moved to another commit. To tell one whose files changed, git runs git
	// status in it, with the submodule's own config, which the builder can
	// write to run a hook or a filter; dirtySubmodules tells that instead.
	out, err := v.git(ctx, "diff", "--raw", "--no-renames", "--ignore-submodules=dirty", "-z", base+"^{commit}", "--")
	if err != nil {
		return nil, err
	}
	// -z gives the modes, object names and status, and the path, as
	// separate NUL-terminated fields.
	fields := nulFields(out)
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("gitdiff: unexpected git diff output %q: %w", out, domain.ErrInvalidValue)
	}
	var changes []domain.FileChange
	for i := 0; i < len(fields); i += 2 {
		f := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if !strings.HasPrefix(fields[i], ":") || len(f) != 5 {
			return nil, fmt.Errorf("gitdiff: unexpected git diff output %q: %w", fields[i], domain.ErrInvalidValue)
		}
		c := domain.FileChange{Path: fields[i+1], Kind: kind(f[4])}
		if f[0] == gitlink || f[1] == gitlink {
			c.Repo = domain.RepoSubmodule
		}
		changes = append(changes, c)
	}
	dirty, err := dirtySubmodules(ctx, v, changes)
	if err != nil {
		return nil, err
	}
	if len(dirty) > 0 {
		// In the path order git diff gives.
		changes = append(changes, dirty...)
		slices.SortStableFunc(changes, func(a, b domain.FileChange) int { return strings.Compare(a.Path, b.Path) })
	}
	untracked, err := untrackedChanges(ctx, v, dir, base)
	if err != nil {
		return nil, err
	}
	return append(changes, untracked...), nil
}

// untrackedChanges are the untracked files of the worktree at dir that the
// .gitignore files of base don't ignore, as additions, and the untracked
// folders with a .git of their own, as nested repos, as the view v sees them.
func untrackedChanges(ctx context.Context, v view, dir, base string) ([]domain.FileChange, error) {
	// ":/" lists from the repo root whatever dir is, and --full-name keeps
	// paths from the root like git diff's. No --exclude-standard: it would
	// also take .git/info/exclude, core.excludesFile and the branch's own
	// .gitignore files, which the builder can all write to hide a file.
	out, err := v.git(ctx, "ls-files", "--others", "--full-name", "-z", "--", ":/")
	if err != nil {
		return nil, err
	}
	untracked := nulFields(out)
	ignored, err := ignoredAtBase(ctx, v.home, dir, base, untracked)
	if err != nil {
		return nil, err
	}
	var changes []domain.FileChange
	for _, p := range untracked {
		// Without --directory, git ends an untracked entry in "/" only for a
		// folder that is a repo of its own.
		if strings.HasSuffix(p, "/") {
			changes = append(changes, domain.FileChange{Path: p, Kind: domain.ChangeAdded, Repo: domain.RepoNested})
			continue
		}
		if !ignored[p] {
			changes = append(changes, domain.FileChange{Path: p, Kind: domain.ChangeAdded})
		}
	}
	return changes, nil
}

// Lines counts the lines the worktree at dir, committed or not, added and
// deleted against base, as Changes takes it: git's numstat of the tracked
// files, plus every line of each untracked file Changes lists as added. A
// binary file and a repo of its own count no lines.
func (d Differ) Lines(ctx context.Context, job domain.JobID, dir, base string) (int, error) {
	if !objectName(base) {
		return 0, fmt.Errorf("gitdiff: base %q is not a full commit hash: %w", base, domain.ErrInvalidValue)
	}
	home, err := d.home(job)
	if err != nil {
		return 0, err
	}
	v, err := newView(ctx, home, dir)
	if err != nil {
		return 0, err
	}
	defer v.close()
	// As in changes, git must not run git status in a submodule.
	out, err := v.git(ctx, "diff", "--numstat", "--no-renames", "--ignore-submodules=dirty", "-z", base+"^{commit}", "--")
	if err != nil {
		return 0, err
	}
	lines := 0
	for _, f := range nulFields(out) {
		added, rest, _ := strings.Cut(f, "\t")
		deleted, _, _ := strings.Cut(rest, "\t")
		// numstat prints "-" for a binary file.
		a, errA := strconv.Atoi(added)
		r, errR := strconv.Atoi(deleted)
		if errA == nil && errR == nil {
			lines += a + r
		}
	}
	untracked, err := untrackedChanges(ctx, v, dir, base)
	if err != nil {
		return 0, err
	}
	for _, c := range untracked {
		if c.Repo != "" {
			continue
		}
		content, err := d.InWorktree(ctx, job, dir, c.Path)
		if err != nil {
			return 0, err
		}
		if !strings.ContainsRune(content, 0) {
			lines += strings.Count(content, "\n")
			if content != "" && !strings.HasSuffix(content, "\n") {
				lines++
			}
		}
	}
	return lines, nil
}

// gitlink is the mode git keeps a submodule's commit with.
const gitlink = "160000"

// ignoredAtBase is the set of paths, from the repo root, that the .gitignore
// files of base ignore. git reads ignore rules only from a worktree, so the
// base's are written to a scratch repo of their own and paths are checked
// there, with no exclude file and no core.excludesFile.
func ignoredAtBase(ctx context.Context, home, dir, base string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := git(ctx, home, dir, "ls-tree", "-r", "-z", "--full-tree", base+"^{commit}")
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "gitdiff-ignore-")
	if err != nil {
		return nil, fmt.Errorf("gitdiff: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	found := false
	for _, entry := range nulFields(out) {
		meta, p, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("gitdiff: unexpected git ls-tree output %q: %w", entry, domain.ErrInvalidValue)
		}
		// git doesn't follow a .gitignore that is a symlink, so neither does this.
		if path.Base(p) != ".gitignore" || (f[0] != "100644" && f[0] != "100755") {
			continue
		}
		rules, err := git(ctx, home, dir, "cat-file", "blob", f[2])
		if err != nil {
			return nil, err
		}
		target := filepath.Join(scratch, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("gitdiff: %w", err)
		}
		if err := os.WriteFile(target, []byte(rules), 0o600); err != nil {
			return nil, fmt.Errorf("gitdiff: %w", err)
		}
		found = true
	}
	if !found {
		return nil, nil
	}
	// No template: one could bring an exclude file or a config.
	if _, err := git(ctx, home, scratch, "init", "-q", "--template="); err != nil {
		return nil, err
	}
	// check-ignore reads each path as a pathspec, so one starting with :/ or
	// :(top) would be matched from the root; a leading ./ makes it plain.
	var stdin strings.Builder
	for _, p := range paths {
		stdin.WriteString("./" + p + "\x00")
	}
	out, err = run(ctx, home, scratch, stdin.String(), nil, "-c", "core.excludesFile="+os.DevNull, "check-ignore", "--no-index", "--stdin", "-z")
	// check-ignore exits 1 when it ignores none of the paths.
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ignored := map[string]bool{}
	for _, p := range nulFields(out) {
		ignored[strings.TrimPrefix(p, "./")] = true
	}
	return ignored, nil
}

// Flagged lists the files the index of the worktree at dir marks
// skip-worktree or assume-unchanged, one entry per flag: git diff and git
// status skip their worktree changes. Changes sees them anyway, as its index
// has no flags, but the flags still tell that the builder hid something.
// dir must be the worktree's root: git looks for the repo in dir alone.
func (d Differ) Flagged(ctx context.Context, job domain.JobID, dir string) ([]domain.FlaggedFile, error) {
	home, err := d.home(job)
	if err != nil {
		return nil, err
	}
	// -v tags each file as -t does, with S for skip-worktree, and in
	// lowercase when it is assume-unchanged.
	out, err := git(ctx, home, dir, "ls-files", "-v", "--full-name", "-z", "--", ":/")
	if err != nil {
		return nil, err
	}
	var flagged []domain.FlaggedFile
	for _, f := range nulFields(out) {
		tag, p, ok := strings.Cut(f, " ")
		if !ok || len(tag) != 1 {
			return nil, fmt.Errorf("gitdiff: unexpected git ls-files output %q: %w", f, domain.ErrInvalidValue)
		}
		if strings.EqualFold(tag, "S") {
			flagged = append(flagged, domain.FlaggedFile{Path: p, Flag: domain.FlagSkipWorktree})
		}
		if tag != strings.ToUpper(tag) {
			flagged = append(flagged, domain.FlaggedFile{Path: p, Flag: domain.FlagAssumeUnchanged})
		}
	}
	return flagged, nil
}

// AtBase is the content of file, by its path from the repo root, in base,
// the full object name of a commit, as Changes takes it, from the repo of
// the worktree whose root is dir, as Flagged takes it.
func (d Differ) AtBase(ctx context.Context, job domain.JobID, dir, base, file string) (string, error) {
	if !objectName(base) {
		return "", fmt.Errorf("gitdiff: base %q is not a full commit hash: %w", base, domain.ErrInvalidValue)
	}
	home, err := d.home(job)
	if err != nil {
		return "", err
	}
	return git(ctx, home, dir, "cat-file", "blob", base+":"+file)
}

// AtBaseIfAny is AtBase for a file base may not have: false when it has
// none, and an ErrInvalidValue error when the path is not a file there, such
// as a folder.
func (d Differ) AtBaseIfAny(ctx context.Context, job domain.JobID, dir, base, file string) (string, bool, error) {
	if !objectName(base) {
		return "", false, fmt.Errorf("gitdiff: base %q is not a full commit hash: %w", base, domain.ErrInvalidValue)
	}
	home, err := d.home(job)
	if err != nil {
		return "", false, err
	}
	entry, err := git(ctx, home, dir, "ls-tree", "-z", "--full-tree", base, "--", file)
	if err != nil {
		return "", false, err
	}
	if entry == "" {
		return "", false, nil
	}
	if f := strings.Fields(entry); len(f) < 2 || f[1] != "blob" {
		return "", false, fmt.Errorf("gitdiff: %s at %s is not a file: %w", file, base, domain.ErrInvalidValue)
	}
	content, err := d.AtBase(ctx, job, dir, base, file)
	return content, err == nil, err
}

// InWorktree is the content of file, by its path from the repo root, in the
// worktree at dir, committed or not. A symlink reads as its target, as git
// keeps it, so AtBase and InWorktree compare alike and nothing outside the
// worktree is read.
func (d Differ) InWorktree(ctx context.Context, job domain.JobID, dir, file string) (string, error) {
	home, err := d.home(job)
	if err != nil {
		return "", err
	}
	out, err := git(ctx, home, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top, err := checkTop(dir, strings.TrimSuffix(out, "\n"))
	if err != nil {
		return "", err
	}
	p := filepath.Join(top, filepath.FromSlash(file))
	info, err := os.Lstat(p)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return "", fmt.Errorf("gitdiff: %w", err)
		}
		return target, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	return string(data), nil
}

// objectName reports whether s is a full SHA-1 or SHA-256 object name in
// lowercase hex, as git rev-parse prints it.
func objectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// nulFields splits git's -z output, which ends in a NUL, into its fields.
func nulFields(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
}

// kind maps git's status letter; a type change or an unmerged file counts as
// modified.
func kind(status string) domain.ChangeKind {
	switch status {
	case "A":
		return domain.ChangeAdded
	case "D":
		return domain.ChangeDeleted
	default:
		return domain.ChangeModified
	}
}

// home is job's HOME in Homes, which git runs with.
func (d Differ) home(job domain.JobID) (string, error) {
	home, err := d.Homes.For(job)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	return home, nil
}

// git runs git in dir with home as its HOME and returns its output
// untrimmed, since -z output ends in a NUL; a failure carries git's own
// message.
func git(ctx context.Context, home, dir string, args ...string) (string, error) {
	return run(ctx, home, dir, "", nil, args...)
}

// run is git with stdin as its input, with childenv.Base, home as its HOME
// and env as its environment: whatever git runs from the config the
// worktree shares, such as a hook it defines or a filter, gets no token
// factoryd holds.
// --no-replace-objects: the refs/replace/ refs are shared with the
// worktree, so a builder could make the base commit stand for one of its
// own and hide what it changed. No fsmonitor: it is set in the config the
// worktree shares, and one that reports no changes makes git trust the index
// over the files. No hooks folder: .git/hooks and core.hooksPath are the
// builder's to write, and git runs a hook whenever it writes the index, as
// git diff does to refresh it; hooks the config defines by name still run.
// GIT_NO_LAZY_FETCH: with a promisor remote in that config, git would fetch
// an object missing from the store over a transport the builder picked.
// No global or system config, and no attributes file, which git reads from
// HOME/.config/git even then: the job's HOME is the builder's to write, and
// a .gitconfig there could, say, give the scratch repo of ignoredAtBase a
// template whose core.worktree points at the job's .gitignore files.
// GIT_CEILING_DIRECTORIES at dir's parent: with the worktree's .git deleted,
// git would find the repo the worktree sits in, the main checkout, and read
// its index and its files as the worktree's.
func run(ctx context.Context, home, dir, stdin string, env []string, args ...string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(childenv.Base(home), "GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(abs))
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitdiff: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
