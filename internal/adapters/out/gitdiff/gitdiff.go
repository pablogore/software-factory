// Package gitdiff lists the files a job's worktree changed against its base,
// for the gate's diff rules.
package gitdiff

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// Differ reads diffs with the git of factoryd's environment.
type Differ struct{}

// Changes lists the files that differ between the worktree at dir,
// committed or not, and base, the full object name of the commit its branch
// was made from. base is never a ref: a ref can be moved from the worktree,
// so a diff against one could hide what the job changed. Renames come as a
// deletion and an addition. An untracked file that .gitignore doesn't ignore
// comes last as an addition: the builder touched it even though no diff
// shows it.
func (Differ) Changes(ctx context.Context, dir, base string) ([]domain.FileChange, error) {
	if base == "" {
		return nil, fmt.Errorf("gitdiff: %w: base", domain.ErrMissingField)
	}
	if !objectName(base) {
		return nil, fmt.Errorf("gitdiff: base %q is not a full commit hash: %w", base, domain.ErrInvalidValue)
	}
	// ^{commit} makes git refuse an object that is not a commit, or not there.
	out, err := git(ctx, dir, "diff", "--name-status", "--no-renames", "-z", base+"^{commit}", "--")
	if err != nil {
		return nil, err
	}
	// -z gives status and path as separate NUL-terminated fields.
	fields := nulFields(out)
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("gitdiff: unexpected git diff output %q: %w", out, domain.ErrInvalidValue)
	}
	var changes []domain.FileChange
	for i := 0; i < len(fields); i += 2 {
		changes = append(changes, domain.FileChange{Path: fields[i+1], Kind: kind(fields[i])})
	}
	// ":/" lists from the repo root whatever dir is, and --full-name keeps
	// paths from the root like git diff's.
	untracked, err := git(ctx, dir, "ls-files", "--others", "--exclude-standard", "--full-name", "-z", "--", ":/")
	if err != nil {
		return nil, err
	}
	for _, p := range nulFields(untracked) {
		changes = append(changes, domain.FileChange{Path: p, Kind: domain.ChangeAdded})
	}
	return changes, nil
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

// git runs git in dir and returns its output untrimmed, since -z output ends
// in a NUL; a failure carries git's own message. --no-replace-objects: the
// refs/replace/ refs are shared with the worktree, so a builder could make
// the base commit stand for one of its own and hide what it changed.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitdiff: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
