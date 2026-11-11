//go:build unix

package wiring_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/wiring"
)

// skipRoles are review roles as their files declare them: one with no when,
// one whose paths the diff meets, one whose paths it doesn't, one whose diff
// size it doesn't, and one on labels, which a job can't evaluate yet.
var skipRoles = []string{
	"name: reviewer-code\nkind: review\nmode: blocking\n",
	"name: reviewer-api\nkind: review\nmode: blocking\nwhen:\n  any:\n    - paths: [\"internal/**\"]\n",
	"name: reviewer-ui\nkind: review\nmode: blocking\nwhen:\n  any:\n    - paths: [\"ui/**\"]\n",
	"name: reviewer-big\nkind: review\nmode: blocking\nwhen:\n  any:\n    - diff_lines: \">= 500\"\n",
	"name: reviewer-sec\nkind: review\nmode: blocking\nwhen:\n  any:\n    - labels: [security]\n",
}

// skipPipeline is a pipeline whose review stage runs skipRoles.
type skipPipeline struct{ roles []domain.Role }

func (p skipPipeline) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name != "default" {
		return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
	}
	var names []string
	for _, r := range p.roles {
		names = append(names, r.Name)
	}
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: names, OnChanges: "build"},
		{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
	}, Roles: p.roles}, nil
}

// changedRepo is a git repo with README.md committed, and a branch off it
// that commits internal/api.go and leaves internal/new.go untracked: three
// lines in all. It returns the repo and its base commit.
func changedRepo(ctx *specs.Context) (string, string) {
	dir := ctx.Testing().TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		ctx.Expect(err).To(specs.BeNil())
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		ctx.Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(specs.BeNil())
		ctx.Expect(os.WriteFile(path, []byte(content), 0o600)).To(specs.BeNil())
	}
	run("init", "-q")
	write("README.md", "readme\n")
	run("add", ".")
	run("commit", "-qm", "base")
	base := run("rev-parse", "HEAD")
	run("switch", "-q", "-c", "factory/j-1")
	write("internal/api.go", "package internal\n\nfunc API() {}\n")
	run("add", ".")
	run("commit", "-qm", "api")
	write("internal/new.go", "package internal\n")
	ctx.Expect(run("diff", "--stat", base)).To(specs.Contain("internal/api.go"))
	return dir, base
}

func TestDemoReviewSkip(t *testing.T) {
	specs.Describe(t, "a review round on a job's real diff", func(s *specs.Spec) {
		bg := context.Background()

		s.It("runs the roles whose when applies, records the skipped ones with their reason and ends the round on the verdicts of those that ran", func(ctx *specs.Context) {
			var roles []domain.Role
			for _, f := range skipRoles {
				r, err := configyaml.LoadRole([]byte(f))
				ctx.Expect(err).To(specs.BeNil())
				roles = append(roles, r)
			}
			p := skipPipeline{roles: roles}
			dir, base := changedRepo(ctx)
			store, err := memstore.New()
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.Create(bg, domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: domain.StageGate, Round: 1})).To(specs.BeNil())
			ctx.Expect(store.RecordBase(bg, "j-1", base)).To(specs.BeNil())
			homes := childenv.Homes{Root: ctx.Testing().TempDir()}

			g := wiring.Gate(store, p, oneDir(dir), store, fixedClock{}, gatelock.New(), homes, []string{"true"}, nil, 0)
			_, err = g.Run(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())

			hist, err := store.History(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			var skips []domain.ReviewSkipped
			for _, r := range hist {
				if e, ok := r.Event.(domain.ReviewSkipped); ok {
					skips = append(skips, e)
				}
			}
			at := fixedClock{}.Now()
			ctx.Expect(skips).ToEqual([]domain.ReviewSkipped{
				{Job: "j-1", Round: 1, Role: "reviewer-ui", Reason: "when does not apply: no changed file matches paths ui/**", At: at},
				{Job: "j-1", Round: 1, Role: "reviewer-big", Reason: "when does not apply: the diff has 4 lines, not >= 500", At: at},
			})
			job, v, err := store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("review")
			ctx.Expect(job.Skipped).ToEqual([]string{"reviewer-ui", "reviewer-big"})

			pl, err := p.Pipeline(bg, "default")
			ctx.Expect(err).To(specs.BeNil())
			for _, role := range []string{"reviewer-code", "reviewer-api", "reviewer-sec"} {
				ev := domain.ResultSubmitted{Job: "j-1", Role: role, Result: domain.ReviewResult{Verdict: domain.ReviewPass}, At: at}
				_, v, err = store.Apply(bg, "j-1", v, pl, ev)
				ctx.Expect(err).To(specs.BeNil())
			}
			job, _, err = store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("approve")
		})
	})
}
