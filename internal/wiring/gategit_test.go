//go:build unix

package wiring_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/wiring"
)

// gateToken is a credential factoryd holds, in its environment for the test.
const gateToken = "ghp_factoryd_fake_gate_token"

// hookedRepo is a git repo with data.txt committed, touched since, so the
// gate's git diff rewrites the index, and a hook in .git/hooks and one its
// config defines, each writing its environment to a marker of its own. It
// returns the repo, its commit and the two markers.
func hookedRepo(ctx *specs.Context) (string, string, string, string) {
	dir := ctx.Testing().TempDir()
	markers := ctx.Testing().TempDir()
	folder, config := filepath.Join(markers, "folder"), filepath.Join(markers, "config")
	ctx.Expect(os.WriteFile(filepath.Join(dir, "data.txt"), []byte("data\n"), 0o600)).To(specs.BeNil())
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "base"},
		{"config", "hook.evil.command", "env > " + config + "; true"},
		{"config", "hook.evil.event", "post-index-change"},
	} {
		ctx.Expect(exec.Command("git", append([]string{"-C", dir}, args...)...).Run()).To(specs.BeNil())
	}
	hook := filepath.Join(dir, ".git", "hooks", "post-index-change")
	ctx.Expect(os.WriteFile(hook, []byte("#!/bin/sh\nenv > "+folder+"\n"), 0o700)).To(specs.BeNil())
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	ctx.Expect(err).To(specs.BeNil())
	later := time.Now().Add(time.Hour)
	ctx.Expect(os.Chtimes(filepath.Join(dir, "data.txt"), later, later)).To(specs.BeNil())
	return dir, strings.TrimSpace(string(out)), folder, config
}

func TestDemoGateGit(t *testing.T) {
	specs.Describe(t, "the git the gate runs on a job's worktree", func(s *specs.Spec) {
		s.It("runs no hook from .git/hooks, nor one the config defines", func(ctx *specs.Context) {
			ctx.Testing().Setenv("GITHUB_TOKEN", gateToken)
			bg := context.Background()
			dir, base, folder, config := hookedRepo(ctx)
			store, err := memstore.New()
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.Create(bg, domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
			ctx.Expect(store.RecordBase(bg, "j-1", base)).To(specs.BeNil())
			homes := childenv.Homes{Root: ctx.Testing().TempDir()}

			g := wiring.Gate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, gatelock.New(), homes, []string{"true"}, nil, 0)
			_, err = g.Check(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())

			_, err = os.Stat(folder)
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
			_, err = os.Stat(config)
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
		})
	})
}
