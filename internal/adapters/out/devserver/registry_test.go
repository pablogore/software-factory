//go:build unix

package devserver_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/devserver"
)

func groupGone(pgid int) bool {
	return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

func records(ctx *specs.Context, dir string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	ctx.Expect(err).To(specs.BeNil())
	return files
}

func TestRegistered(t *testing.T) {
	specs.Describe(t, "devserver.Registered", func(s *specs.Spec) {
		bg := context.Background()

		s.It("kills, with their whole group, the dev servers an earlier run left behind", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			crashed, err := devserver.Registered(bg, 200*time.Millisecond, dir)
			ctx.Expect(err).To(specs.BeNil())
			wt := worktree(ctx, "job-1")
			ctx.Expect(crashed.Start(bg, wt, "build", 5100, sh(tree))).To(specs.BeNil())
			running := pids(ctx, wt.Path, treePids...)
			ctx.Expect(records(ctx, dir)).To(specs.HaveLen(1))

			_, err = devserver.Registered(bg, 200*time.Millisecond, dir)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(eventually(func() bool { return noneAlive(running) })).To(specs.BeTrue())
			ctx.Expect(groupGone(running[0])).To(specs.BeTrue())
			ctx.Expect(records(ctx, dir)).To(specs.BeEmpty())
		})

		s.It("leaves alone a process that got a recorded pid later, dropping the record", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			other := exec.Command("sleep", "300")
			other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			ctx.Expect(other.Start()).To(specs.BeNil())
			ctx.Testing().Cleanup(func() { _ = syscall.Kill(-other.Process.Pid, syscall.SIGKILL); _ = other.Wait() })
			pgid := other.Process.Pid
			record := `{"pgid":` + strconv.Itoa(pgid) + `,"started":"Mon Jan  1 00:00:00 2001"}`
			ctx.Expect(os.WriteFile(filepath.Join(dir, strconv.Itoa(pgid)+".json"), []byte(record), 0o600)).To(specs.BeNil())

			_, err := devserver.Registered(bg, 200*time.Millisecond, dir)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(alive(pgid)).To(specs.BeTrue())
			ctx.Expect(records(ctx, dir)).To(specs.BeEmpty())
		})

		s.It("drops the record of a server once it is stopped", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			srv, err := devserver.Registered(bg, 200*time.Millisecond, dir)
			ctx.Expect(err).To(specs.BeNil())
			wt := worktree(ctx, "job-1")
			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh("exec sleep 300"))).To(specs.BeNil())
			ctx.Expect(records(ctx, dir)).To(specs.HaveLen(1))

			ctx.Expect(srv.StopJob(bg, "job-1")).To(specs.BeNil())

			ctx.Expect(records(ctx, dir)).To(specs.BeEmpty())
		})
	})
}
