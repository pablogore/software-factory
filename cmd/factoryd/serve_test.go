//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/worker"
	"github.com/pablogore/software-factory/internal/wiring"
)

// factoryd is this test binary run as factoryd with args, in its own
// directory dir.
func factoryd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), mainEnv+"=1")
	cmd.Dir = dir
	return cmd
}

// serveArgs runs serve over the repo's own configuration, with its store
// and dev server records in dir.
func serveArgs(dir string, extra ...string) []string {
	root, _ := filepath.Abs("../..")
	return append([]string{"serve", "--config", root, "--store", filepath.Join(dir, "factoryd.db"), "--repo", dir,
		"--dev-state", filepath.Join(dir, "devservers"), "--tick", "10ms", "--shutdown-grace", "5s", "--listen", "127.0.0.1:0"}, extra...)
}

// seedServeStore saves a job in build whose builder handed in a ready
// build, so its events wait for the dev server subscriber.
func seedServeStore(ctx *specs.Context, path string) {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	defer func() { ctx.Expect(st.Close()).To(specs.BeNil()) }()
	ctx.Expect(st.Create(bg, domain.Job{ID: "demo-1", Title: "Demo job", Pipeline: "demo", Stage: "build", Round: 1})).To(specs.BeNil())
	p, err := demoPipelines{}.Pipeline(bg, "demo")
	ctx.Expect(err).To(specs.BeNil())
	_, v, err := st.Load(bg, "demo-1")
	ctx.Expect(err).To(specs.BeNil())
	ready := domain.ResultSubmitted{Job: "demo-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: time.Now()}
	_, _, err = st.Apply(bg, "demo-1", v, p, ready)
	ctx.Expect(err).To(specs.BeNil())
}

func TestDemoServe(t *testing.T) {
	specs.Describe(t, "factoryd serve", func(s *specs.Spec) {
		s.It("starts, hands the pending events on, and on SIGTERM stops with its store closed and its events kept", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			db := filepath.Join(dir, "factoryd.db")
			seedServeStore(ctx, db)
			cmd := factoryd(dir, serveArgs(dir, "--budget", "5")...)
			stdout, err := cmd.StdoutPipe()
			ctx.Expect(err).To(specs.BeNil())
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			ctx.Expect(cmd.Start()).To(specs.BeNil())
			lines := bufio.NewReader(stdout)

			first, err := lines.ReadString('\n')
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(first).To(specs.StartWith("factoryd serving " + db))
			ctx.Expect(cmd.Process.Signal(syscall.SIGTERM)).To(specs.BeNil())
			rest, err := io.ReadAll(lines)
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(cmd.Wait()).To(specs.BeNil())
			ctx.Expect(stderr.String()).To(specs.BeEmpty())
			ctx.Expect(strings.TrimSpace(string(rest))).ToEqual("factoryd stopped; store closed")
			st, err := sqlite.Open(context.Background(), db)
			ctx.Expect(err).To(specs.BeNil())
			defer func() { _ = st.Close() }()
			hist, err := st.History(context.Background(), "demo-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.Not(specs.BeEmpty()))
			left, err := st.Pending(context.Background(), wiring.DevServerSubscriber, worker.DevServerEvents)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(left).To(specs.BeEmpty())
		})

		s.It("exits non-zero naming the piece that failed to start", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			cmd := factoryd(dir, serveArgs(dir)...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			err := cmd.Run()

			var exit *exec.ExitError
			ctx.Expect(errors.As(err, &exit)).To(specs.BeTrue())
			ctx.Expect(exit.ExitCode()).ToEqual(1)
			ctx.Expect(stderr.String()).To(specs.StartWith("factoryd serve: scheduler: "))
			ctx.Expect(stdout.String()).To(specs.BeEmpty())
		})
	})
}

func TestServeListen(t *testing.T) {
	specs.Describe(t, "factoryd serve on an address that is not loopback", func(s *specs.Spec) {
		s.It("starts all the same and warns which address it listens on", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			// The last --listen wins over serveArgs' loopback one.
			cmd := factoryd(dir, serveArgs(dir, "--budget", "5", "--listen", "0.0.0.0:0")...)
			stdout, err := cmd.StdoutPipe()
			ctx.Expect(err).To(specs.BeNil())
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			ctx.Expect(cmd.Start()).To(specs.BeNil())
			lines := bufio.NewReader(stdout)

			warning, err := lines.ReadString('\n')
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(warning).To(specs.StartWith("warning: the worker API listens on 0.0.0.0:0 ("))
			ctx.Expect(warning).To(specs.Contain("not a loopback address"))
			serving, err := lines.ReadString('\n')
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(serving).To(specs.StartWith("factoryd serving "))
			ctx.Expect(cmd.Process.Signal(syscall.SIGTERM)).To(specs.BeNil())
			_, err = io.ReadAll(lines)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(cmd.Wait()).To(specs.BeNil())
			ctx.Expect(stderr.String()).To(specs.BeEmpty())
		})
	})
}
