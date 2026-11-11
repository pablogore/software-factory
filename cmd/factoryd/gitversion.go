package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// minGit is the oldest git factoryd runs with: the defenses its git calls
// rely on (GIT_ATTR_SOURCE, --path-format, hook.<name>.command) need it, and
// an older git ignores some of them without a word.
var minGit = [2]int{2, 44}

// checkGitVersion fails unless the git first in PATH, the one factoryd runs
// afterwards, is minGit or newer.
func checkGitVersion(ctx context.Context) error {
	need := fmt.Sprintf("factoryd needs git %d.%d or newer", minGit[0], minGit[1])
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("git not found in PATH: %s", need)
	}
	if err != nil {
		return fmt.Errorf("git --version: %w: %s", err, need)
	}
	found := strings.TrimSpace(string(out))
	major, minor, ok := parseGitVersion(found)
	if !ok {
		return fmt.Errorf("can't read git's version from %q: %s", found, need)
	}
	if major < minGit[0] || major == minGit[0] && minor < minGit[1] {
		return fmt.Errorf("found %s: %s", found, need)
	}
	return nil
}

// parseGitVersion reads major and minor from git --version's output, such
// as "git version 2.39.3 (Apple Git-145)" or "git version 2.45.1.windows.1".
func parseGitVersion(s string) (major, minor int, ok bool) {
	rest, found := strings.CutPrefix(s, "git version ")
	if !found {
		return 0, 0, false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, 0, false
	}
	parts := strings.SplitN(fields[0], ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
