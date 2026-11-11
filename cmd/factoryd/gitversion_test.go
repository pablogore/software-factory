//go:build unix

package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

// fakeGit is a PATH holding only a git that answers --version with version
// and hands every other command to the real git.
func fakeGit(ctx *specs.Context, version string) string {
	gitPath, err := exec.LookPath("git")
	ctx.Expect(err).To(specs.BeNil())
	bin := ctx.Testing().TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo '" + version + "'; exit 0; fi\nexec " + gitPath + " \"$@\"\n"
	ctx.Expect(os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755)).To(specs.BeNil())
	return bin
}

// serveWithPath is serve run as factoryd with path as its PATH.
func serveWithPath(dir, path string) *exec.Cmd {
	cmd := factoryd(dir, serveArgs(dir, "--budget", "5")...)
	cmd.Env = append(cmd.Env, "PATH="+path)
	return cmd
}

// gitVersionRow is a git factoryd refuses: version is what it answers to
// --version, empty for no git at all, and found how the error names it.
type gitVersionRow struct {
	name, version, found string
}

func TestDemoGitVersion(t *testing.T) {
	specs.Describe(t, "factoryd serve and the git version", func(s *specs.Spec) {
		s.It("starts as before with git 2.44", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			cmd := serveWithPath(dir, fakeGit(ctx, "git version 2.44.0"))
			stdout, err := cmd.StdoutPipe()
			ctx.Expect(err).To(specs.BeNil())
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			ctx.Expect(cmd.Start()).To(specs.BeNil())
			lines := bufio.NewReader(stdout)

			first, err := lines.ReadString('\n')
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(first).To(specs.StartWith("factoryd serving "))
			ctx.Expect(cmd.Process.Signal(syscall.SIGTERM)).To(specs.BeNil())
			_, err = io.ReadAll(lines)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(cmd.Wait()).To(specs.BeNil())
			ctx.Expect(stderr.String()).To(specs.BeEmpty())
		})

		refused := []gitVersionRow{
			{"git 2.43", "git version 2.43.7 (Apple Git-150)", "found git version 2.43.7 (Apple Git-150)"},
			{"git 1.99", "git version 1.99.0", "found git version 1.99.0"},
			{"a version it can't read", "git version two", "can't read git's version from \"git version two\""},
			{"no git in PATH", "", "git not found in PATH"},
		}
		specs.Table(s, refused, func(r gitVersionRow) string {
			return "refuses to start, before it opens the store or makes a worktree, with " + r.name
		}, func(ctx *specs.Context, r gitVersionRow) {
			path := ctx.Testing().TempDir()
			if r.version != "" {
				path = fakeGit(ctx, r.version)
			}
			dir := ctx.Testing().TempDir()
			cmd := serveWithPath(dir, path)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			err := cmd.Run()

			var exit *exec.ExitError
			ctx.Expect(errors.As(err, &exit)).To(specs.BeTrue())
			ctx.Expect(exit.ExitCode()).ToEqual(1)
			ctx.Expect(stderr.String()).ToEqual("factoryd serve: " + r.found + ": factoryd needs git 2.44 or newer\n")
			ctx.Expect(stdout.String()).To(specs.BeEmpty())
			left, err := os.ReadDir(dir)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(left).To(specs.BeEmpty())
		})
	})
}

func TestGitVersionParse(t *testing.T) {
	specs.Describe(t, "parseGitVersion", func(s *specs.Spec) {
		type row struct {
			out          string
			major, minor int
			ok           bool
		}
		rows := []row{
			{"git version 2.44.0", 2, 44, true},
			{"git version 2.39.3 (Apple Git-145)", 2, 39, true},
			{"git version 2.45.1.windows.1", 2, 45, true},
			{"git version 3.0", 3, 0, true},
			{"git version 2", 0, 0, false},
			{"git version ", 0, 0, false},
			{"version 2.44.0", 0, 0, false},
		}
		specs.Table(s, rows, func(r row) string {
			return "reads " + strings.TrimSpace(r.out)
		}, func(ctx *specs.Context, r row) {
			major, minor, ok := parseGitVersion(r.out)
			ctx.Expect([]any{major, minor, ok}).ToEqual([]any{r.major, r.minor, r.ok})
		})
	})
}
