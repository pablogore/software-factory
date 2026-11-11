// Package gitsafe runs git on a job's own repo, where it must write: make,
// back up and remove a job's worktree, or check a commit out apart. The
// repo's config, hooks folder and .gitattributes are the builder's to write,
// and git runs what they name when it updates a ref, checks files out or
// stages them; here git runs with none of it and with no token factoryd
// holds.
package gitsafe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// maxIncludes is how deep includes go, git's own limit.
const maxIncludes = 10

// Run runs git in dir with childenv.Base, home as its HOME and env added,
// and returns its output untrimmed. No hook runs, whether from the hooks
// folder, core.hooksPath or the config, and no clean, smudge or process
// filter: files are checked out and staged as git keeps them.
// --no-replace-objects: refs/replace/ is shared with every worktree, so a
// job could make a commit stand for one of its own. No fsmonitor, which git
// status would run. No submodule.recurse: git would run in a submodule, with
// its own config. GIT_NO_LAZY_FETCH: with a promisor remote in the config,
// git would fetch a missing object over a transport the builder picked. No
// global or system config, and no attributes file: git reads them from HOME,
// the job's HOME, which the builder writes.
func Run(ctx context.Context, home, dir string, env []string, args ...string) (string, error) {
	off, err := disabled(ctx, home, dir)
	if err != nil {
		return "", err
	}
	return run(ctx, home, dir, append(slices.Clone(env), off...), args...)
}

func run(ctx context.Context, home, dir string, env []string, args ...string) (string, error) {
	flags := []string{"--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.attributesFile=" + os.DevNull, "-c", "submodule.recurse=false"}
	cmd := exec.CommandContext(ctx, "git", append(flags, args...)...)
	cmd.Dir = dir
	cmd.Env = append(childenv.Base(home), "GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// disabled is the config, as GIT_CONFIG_COUNT variables, that turns off
// every hook and filter the config of the repo at dir names. core.hooksPath
// doesn't turn off a hook the config defines by name, and a filter can be
// named in .gitattributes, in info/attributes or in the branch checked out,
// so each one the config defines is emptied instead. The config is read file
// by file, with every file it includes whatever the include's condition: a
// condition such as onbranch could hold in a worktree git runs in for the
// command and not here.
func disabled(ctx context.Context, home, dir string) ([]string, error) {
	out, err := run(ctx, home, dir, nil, "rev-parse", "--path-format=absolute", "--git-common-dir", "--git-dir")
	if err != nil {
		return nil, err
	}
	dirs := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(dirs) != 2 {
		return nil, fmt.Errorf("gitsafe: unexpected git rev-parse output %q", out)
	}
	files := []string{filepath.Join(dirs[0], "config"), filepath.Join(dirs[1], "config.worktree")}
	hooks, filters := map[string]bool{}, map[string]bool{}
	read := map[string]bool{}
	for depth := 0; len(files) > 0; depth++ {
		if depth > maxIncludes {
			return nil, fmt.Errorf("gitsafe: the config of %s includes files more than %d deep", dir, maxIncludes)
		}
		var next []string
		for _, f := range files {
			if read[f] {
				continue
			}
			read[f] = true
			included, err := scan(ctx, home, dir, f, hooks, filters)
			if err != nil {
				return nil, err
			}
			next = append(next, included...)
		}
		files = next
	}
	var kv [][2]string
	for _, name := range sortedKeys(hooks) {
		kv = append(kv, [2]string{"hook." + name + ".enabled", "false"})
	}
	for _, name := range sortedKeys(filters) {
		for _, k := range []string{"clean", "smudge", "process"} {
			kv = append(kv, [2]string{"filter." + name + "." + k, ""})
		}
		// A required filter with no command fails the checkout.
		kv = append(kv, [2]string{"filter." + name + ".required", "false"})
	}
	if len(kv) == 0 {
		return nil, nil
	}
	env := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(len(kv))}
	for i, p := range kv {
		env = append(env, "GIT_CONFIG_KEY_"+strconv.Itoa(i)+"="+p[0], "GIT_CONFIG_VALUE_"+strconv.Itoa(i)+"="+p[1])
	}
	return env, nil
}

// scan adds to hooks and filters the names of those the config file f
// defines, and returns the files it includes. A file that isn't there
// defines none, as for git.
func scan(ctx context.Context, home, dir, f string, hooks, filters map[string]bool) ([]string, error) {
	out, err := run(ctx, home, dir, nil, "config", "--file", f, "-z", "--get-regexp", `^(hook|filter|include|includeif)\.`)
	// git config exits 1 when no key matches or the file is missing.
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var included []string
	for _, entry := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		section, rest, _ := strings.Cut(key, ".")
		// The subsection is all between the first dot and the last.
		cut := strings.LastIndex(rest, ".")
		switch {
		case section == "hook" && cut > 0:
			hooks[rest[:cut]] = true
		case section == "filter" && cut > 0:
			filters[rest[:cut]] = true
		case (section == "include" || section == "includeif") && strings.HasSuffix(key, ".path") && value != "":
			if p, ok := includePath(home, f, value); ok {
				included = append(included, p)
			}
		}
	}
	return included, nil
}

// includePath is the file an include in the config file f names with
// value, resolved as git does: ~ is home, the HOME git runs with, and a
// relative path is from f's folder. One with a %(prefix)/ is in git's own
// install, out of the builder's reach.
func includePath(home, f, value string) (string, bool) {
	if strings.HasPrefix(value, "%(prefix)/") {
		return "", false
	}
	if strings.HasPrefix(value, "~") {
		name, rest, _ := strings.Cut(value[1:], "/")
		dir := home
		if name != "" {
			u, err := user.Lookup(name)
			if err != nil {
				return "", false
			}
			dir = u.HomeDir
		}
		return filepath.Join(dir, rest), true
	}
	if filepath.IsAbs(value) {
		return value, true
	}
	return filepath.Join(filepath.Dir(f), value), true
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
