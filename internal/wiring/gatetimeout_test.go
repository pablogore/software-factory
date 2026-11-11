//go:build unix

package wiring_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/wiring"
)

func TestDemoGateTimeout(t *testing.T) {
	specs.Describe(t, "the job's gate with a test that never ends", func(s *specs.Spec) {
		bg := context.Background()

		s.It("fails the gate at its limit with a timed out note, leaves no check alive, and the job's next gate and run_gate run normally", func(ctx *specs.Context) {
			dir, base := gitRepo(ctx)
			outside := ctx.Testing().TempDir()
			hang := filepath.Join(outside, "hang")
			pidFile := filepath.Join(outside, "child.pid")
			ctx.Expect(os.WriteFile(hang, nil, 0o600)).To(specs.BeNil())
			// While hang exists the checks leave a child in the background and
			// wait for it, as a hung test does; without it they pass.
			command := []string{"sh", "-c", "if [ -f " + hang + " ]; then sleep 60 >/dev/null 2>&1 & echo $! > " + pidFile + "; wait; fi"}
			store, err := memstore.New()
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.Create(bg, domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: domain.StageGate, Round: 1})).To(specs.BeNil())
			ctx.Expect(store.RecordBase(bg, "j-1", base)).To(specs.BeNil())
			locks := gatelock.New()
			homes := childenv.Homes{Root: ctx.Testing().TempDir()}
			gate := wiring.Gate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, locks, homes, command, nil, 300*time.Millisecond)

			start := time.Now()
			evs, err := gate.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(time.Since(start) < 10*time.Second).To(specs.BeTrue())
			ctx.Expect(names(evs)).ToEqual([]string{"ChecksRan", "RoundStarted", "StageEntered"})
			failed, ok := evs[0].(domain.ChecksRan)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(failed.Run.Passed()).To(specs.BeFalse())
			ctx.Expect(strings.Join(failed.Run.Notes, "\n")).To(specs.Contain("timed out: the checks ran over 300ms and were stopped with their process group"))
			data, err := os.ReadFile(pidFile)
			ctx.Expect(err).To(specs.BeNil())
			child, err := strconv.Atoi(strings.TrimSpace(string(data)))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(syscall.Kill(child, 0)).To(specs.MatchError(syscall.ESRCH))
			job, v, err := store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.Round).ToEqual(2)

			// The test is fixed: run_gate and the job's next gate, under the
			// same locks, run normally.
			ctx.Expect(os.Remove(hang)).To(specs.BeNil())
			runGate := wiring.RunGate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, locks, homes, command, nil, time.Minute, wiring.DefaultRunGateMaxOutput, wiring.DefaultRunGateMaxRuns)
			run, release, err := locks.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			checked, err := runGate.Check(run, "j-1")
			release()
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(checked.Passed()).To(specs.BeTrue())
			p, err := onePipeline{}.Pipeline(bg, "default")
			ctx.Expect(err).To(specs.BeNil())
			_, _, err = store.Apply(bg, "j-1", v, p, domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: fixedClock{}.Now()})
			ctx.Expect(err).To(specs.BeNil())

			evs, err = gate.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(names(evs)).ToEqual([]string{"ChecksRan", "StageEntered"})
			job, _, err = store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("review")
		})

		s.It("gives each command of the gate 20 minutes when no limit is configured", func(ctx *specs.Context) {
			g := wiring.Gate(nil, onePipeline{}, oneDir(""), nil, fixedClock{}, gatelock.New(), childenv.Homes{}, []string{"true"}, nil, 0)

			runner, ok := g.Checks.(checkrun.Runner)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(runner.Timeout).ToEqual(20 * time.Minute)
		})
	})
}
