package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

// git runs git in dir with no user or system config.
func git(ctx *specs.Context, dir string, args ...string) {
	ctx.Testing().Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		ctx.Testing().Log(string(out))
	}
	ctx.Expect(err).To(specs.BeNil())
}

func TestGateDemo(t *testing.T) {
	specs.Describe(t, "factoryd gate-demo", func(s *specs.Spec) {
		s.It("fails a job that touches files outside the build role's allowed paths, naming both, and sends it back to build", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0o600)).To(specs.BeNil())
			git(ctx, dir, "init", "-q", "-b", "main")
			git(ctx, dir, "add", ".")
			git(ctx, dir, "commit", "-q", "-m", "base")
			ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o600)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("new\n"), 0o600)).To(specs.BeNil())
			role := filepath.Join(ctx.Testing().TempDir(), "builder.yaml")
			ctx.Expect(os.WriteFile(role, []byte("name: builder\nkind: build\nmode: blocking\nallowed_paths: [internal/slices/build/]\n"), 0o600)).To(specs.BeNil())

			var out bytes.Buffer
			err := gateDemo(context.Background(), []string{"-dir", dir, "-checks", "true", "-role", role}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("build role builder may touch only [internal/slices/build/]"))
			ctx.Expect(out.String()).To(specs.Contain("rule broken: the diff modifies README.md, outside the allowed paths of build role builder"))
			ctx.Expect(out.String()).To(specs.Contain("rule broken: the diff adds scratch.txt, outside the allowed paths of build role builder"))
			ctx.Expect(out.String()).To(specs.Contain("job gate-demo: stage build, round 2"))
		})

		s.It("with -db, makes the job's worktree first, then fails a commit to the Makefile even after the base ref is moved onto it", func(ctx *specs.Context) {
			bg := context.Background()
			repo := ctx.Testing().TempDir()
			ctx.Expect(os.WriteFile(filepath.Join(repo, "Makefile"), []byte("check:\n"), 0o600)).To(specs.BeNil())
			git(ctx, repo, "init", "-q", "-b", "main")
			git(ctx, repo, "add", ".")
			git(ctx, repo, "commit", "-q", "-m", "base")
			git(ctx, repo, "branch", "arch/base")
			db := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			args := []string{"-dir", repo, "-base", "arch/base", "-db", db, "-checks", "true"}

			var made bytes.Buffer
			ctx.Expect(gateDemo(bg, args, &made)).To(specs.BeNil())
			ctx.Expect(made.String()).To(specs.Contain("job gate-demo made: worktree"))
			wt := filepath.Join(repo, ".worktrees", "gate-demo")
			ctx.Expect(os.WriteFile(filepath.Join(wt, "Makefile"), []byte("check:\n\ttrue\n"), 0o600)).To(specs.BeNil())
			git(ctx, wt, "commit", "-q", "-am", "skip the checks")
			git(ctx, wt, "branch", "-f", "arch/base", "HEAD")

			var out bytes.Buffer
			err := gateDemo(bg, args, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("rule broken: the diff modifies the protected path Makefile"))
			ctx.Expect(out.String()).To(specs.Contain("job gate-demo: stage build, round 2"))
		})

		s.It("with -db, fails a commit that deletes one test and skips another, naming each, and passes once it is reverted", func(ctx *specs.Context) {
			bg := context.Background()
			repo := ctx.Testing().TempDir()
			test := "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n}\n"
			ctx.Expect(os.WriteFile(filepath.Join(repo, "a_test.go"), []byte(test), 0o600)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(repo, "b_test.go"), []byte(test), 0o600)).To(specs.BeNil())
			git(ctx, repo, "init", "-q", "-b", "main")
			git(ctx, repo, "add", ".")
			git(ctx, repo, "commit", "-q", "-m", "base")
			db := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			args := []string{"-dir", repo, "-base", "main", "-db", db, "-checks", "true"}
			ctx.Expect(gateDemo(bg, args, &bytes.Buffer{})).To(specs.BeNil())
			wt := filepath.Join(repo, ".worktrees", "gate-demo")
			git(ctx, wt, "rm", "-q", "a_test.go")
			skipped := "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tt.Skip()\n}\n"
			ctx.Expect(os.WriteFile(filepath.Join(wt, "b_test.go"), []byte(skipped), 0o600)).To(specs.BeNil())
			git(ctx, wt, "commit", "-q", "-am", "drop the tests")

			var failed bytes.Buffer
			ctx.Expect(gateDemo(bg, args, &failed)).To(specs.BeNil())

			ctx.Expect(failed.String()).To(specs.Contain("rule broken: the diff deletes the test file a_test.go"))
			ctx.Expect(failed.String()).To(specs.Contain("rule broken: the diff adds t.Skip to the test file b_test.go"))
			ctx.Expect(failed.String()).To(specs.Contain("job gate-demo: stage build, round 2"))

			git(ctx, wt, "revert", "--no-edit", "HEAD")
			// The job went back to build, so the reverted worktree is gated as a
			// new job against the same base.
			var passed bytes.Buffer
			ctx.Expect(gateDemo(bg, []string{"-dir", wt, "-base", "main", "-checks", "true"}, &passed)).To(specs.BeNil())

			ctx.Expect(passed.String()).To(specs.Not(specs.Contain("rule broken")))
			ctx.Expect(passed.String()).To(specs.Contain("the gate passed"))
		})

		s.It("with -db and -count, fails a commit that deletes a test case, naming its package, and passes once it is restored", func(ctx *specs.Context) {
			bg := context.Background()
			repo := ctx.Testing().TempDir()
			tests := "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n"
			ctx.Expect(os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o600)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(repo, "x_test.go"), []byte(tests), 0o600)).To(specs.BeNil())
			git(ctx, repo, "init", "-q", "-b", "main")
			git(ctx, repo, "add", ".")
			git(ctx, repo, "commit", "-q", "-m", "base")
			db := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			args := []string{"-dir", repo, "-base", "main", "-db", db, "-checks", "true", "-count", "go test -list . ./..."}
			ctx.Expect(gateDemo(bg, args, &bytes.Buffer{})).To(specs.BeNil())
			wt := filepath.Join(repo, ".worktrees", "gate-demo")
			fewer := "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"
			ctx.Expect(os.WriteFile(filepath.Join(wt, "x_test.go"), []byte(fewer), 0o600)).To(specs.BeNil())
			git(ctx, wt, "commit", "-q", "-am", "drop a case")

			var failed bytes.Buffer
			ctx.Expect(gateDemo(bg, args, &failed)).To(specs.BeNil())

			ctx.Expect(failed.String()).To(specs.Contain("rule broken: the package example.com/x has 1 tests, 2 at the base commit"))
			ctx.Expect(failed.String()).To(specs.Contain("job gate-demo: stage build, round 2"))

			git(ctx, wt, "revert", "--no-edit", "HEAD")
			var passed bytes.Buffer
			ctx.Expect(gateDemo(bg, []string{"-dir", wt, "-base", "main", "-checks", "true", "-count", "go test -list . ./..."}, &passed)).To(specs.BeNil())

			ctx.Expect(passed.String()).To(specs.Not(specs.Contain("rule broken")))
			ctx.Expect(passed.String()).To(specs.Not(specs.Contain("note:")))
			ctx.Expect(passed.String()).To(specs.Contain("the gate passed"))
		})

		s.It("with -db and -count go test -json, fails a commit that deletes a subtest or skips one, naming its package, and passes once it is restored", func(ctx *specs.Context) {
			bg := context.Background()
			repo := ctx.Testing().TempDir()
			// Subtests, as go-specs runs each It and table row; st.Skip is
			// not a mark the diff rules see, as SkipIt isn't.
			subtests := func(body string) []byte {
				return []byte("package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n" + body + "}\n")
			}
			both := "\tt.Run(\"one\", func(st *testing.T) {})\n\tt.Run(\"two\", func(st *testing.T) {})\n"
			ctx.Expect(os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o600)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(repo, "x_test.go"), subtests(both), 0o600)).To(specs.BeNil())
			git(ctx, repo, "init", "-q", "-b", "main")
			git(ctx, repo, "add", ".")
			git(ctx, repo, "commit", "-q", "-m", "base")
			db := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			args := []string{"-dir", repo, "-base", "main", "-db", db, "-checks", "true", "-count", "go test -json -count=1 ./..."}
			ctx.Expect(gateDemo(bg, args, &bytes.Buffer{})).To(specs.BeNil())
			wt := filepath.Join(repo, ".worktrees", "gate-demo")
			wtArgs := []string{"-dir", wt, "-base", "main", "-checks", "true", "-count", "go test -json -count=1 ./..."}
			drop := "the package example.com/x has 2 tests, 3 at the base commit"

			ctx.Expect(os.WriteFile(filepath.Join(wt, "x_test.go"), subtests("\tt.Run(\"one\", func(st *testing.T) {})\n"), 0o600)).To(specs.BeNil())
			git(ctx, wt, "commit", "-q", "-am", "drop a subtest")
			var deleted bytes.Buffer
			ctx.Expect(gateDemo(bg, args, &deleted)).To(specs.BeNil())

			ctx.Expect(deleted.String()).To(specs.Contain("rule broken: " + drop))
			ctx.Expect(deleted.String()).To(specs.Contain("job gate-demo: stage build, round 2"))

			skip := "\tt.Run(\"one\", func(st *testing.T) {})\n\tt.Run(\"two\", func(st *testing.T) { st.Skip() })\n"
			ctx.Expect(os.WriteFile(filepath.Join(wt, "x_test.go"), subtests(skip), 0o600)).To(specs.BeNil())
			git(ctx, wt, "commit", "-q", "-am", "skip a subtest")
			var skipped bytes.Buffer
			ctx.Expect(gateDemo(bg, wtArgs, &skipped)).To(specs.BeNil())

			ctx.Expect(skipped.String()).To(specs.Contain("rule broken: " + drop))
			ctx.Expect(skipped.String()).To(specs.Contain("the gate failed"))

			ctx.Expect(os.WriteFile(filepath.Join(wt, "x_test.go"), subtests(both), 0o600)).To(specs.BeNil())
			git(ctx, wt, "commit", "-q", "-am", "restore")
			var passed bytes.Buffer
			ctx.Expect(gateDemo(bg, wtArgs, &passed)).To(specs.BeNil())

			ctx.Expect(passed.String()).To(specs.Not(specs.Contain("rule broken")))
			ctx.Expect(passed.String()).To(specs.Contain("the gate passed"))
		})

		s.It("says the test count rule did not run without -count", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0o600)).To(specs.BeNil())
			git(ctx, dir, "init", "-q", "-b", "main")
			git(ctx, dir, "add", ".")
			git(ctx, dir, "commit", "-q", "-m", "base")

			var out bytes.Buffer
			ctx.Expect(gateDemo(context.Background(), []string{"-dir", dir, "-checks", "true"}, &out)).To(specs.BeNil())

			ctx.Expect(out.String()).To(specs.Contain("note: the test count rule did not run: no test count command is configured"))
			ctx.Expect(out.String()).To(specs.Contain("the gate passed"))
		})

		s.It("fails a new test file not yet committed that adds .only, naming it", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0o600)).To(specs.BeNil())
			git(ctx, dir, "init", "-q", "-b", "main")
			git(ctx, dir, "add", ".")
			git(ctx, dir, "commit", "-q", "-m", "base")
			ctx.Expect(os.MkdirAll(filepath.Join(dir, "ui"), 0o750)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(dir, "ui", "app.test.ts"), []byte("it.only('renders', () => {})\n"), 0o600)).To(specs.BeNil())

			var out bytes.Buffer
			err := gateDemo(context.Background(), []string{"-dir", dir, "-checks", "true"}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("rule broken: the diff adds .only to the test file ui/app.test.ts"))
			ctx.Expect(out.String()).To(specs.Contain("job gate-demo: stage build, round 2"))
		})

		s.It("refuses a role file that is not a build role", func(ctx *specs.Context) {
			role := filepath.Join(ctx.Testing().TempDir(), "reviewer.yaml")
			ctx.Expect(os.WriteFile(role, []byte("name: reviewer-code\nkind: review\nmode: blocking\n"), 0o600)).To(specs.BeNil())

			err := gateDemo(context.Background(), []string{"-checks", "true", "-role", role}, &bytes.Buffer{})

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})
	})
}
