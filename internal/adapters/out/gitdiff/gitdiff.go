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

// Changes lists the tracked files that differ between the worktree at dir,
// committed or not, and where its branch left base. Renames come as a
// deletion and an addition. Untracked files are not listed.
func (Differ) Changes(ctx context.Context, dir, base string) ([]domain.FileChange, error) {
	if base == "" {
		return nil, fmt.Errorf("gitdiff: %w: base", domain.ErrMissingField)
	}
	fork, err := git(ctx, dir, "merge-base", base, "HEAD")
	if err != nil {
		return nil, err
	}
	out, err := git(ctx, dir, "diff", "--name-status", "--no-renames", "-z", strings.TrimSpace(fork), "--")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	// -z gives status and path as separate NUL-terminated fields.
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("gitdiff: unexpected git diff output %q: %w", out, domain.ErrInvalidValue)
	}
	changes := make([]domain.FileChange, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		changes = append(changes, domain.FileChange{Path: fields[i+1], Kind: kind(fields[i])})
	}
	return changes, nil
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
// in a NUL; a failure carries git's own message.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitdiff: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
