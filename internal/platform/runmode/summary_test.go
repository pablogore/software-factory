package runmode_test

import (
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/runmode"
)

type whyRow struct {
	name  string
	order queue.Order
	next  int
	want  string
}

func TestSummarize(t *testing.T) {
	specs.Describe(t, "runmode.Summarize", func(s *specs.Spec) {
		s.When("an issue was a job on its own", func(s *specs.Spec) {
			s.It("sums it up with no PR and no next issue when none waits", func(ctx *specs.Context) {
				r := alone(4, domain.StageNeedsInfo)
				got := runmode.Summarize(r, "4", nil, queue.Order{})
				ctx.Expect(got).ToEqual(runmode.Summary{
					Issue: 4, Title: "Issue 4", Stage: domain.StageNeedsInfo,
					PRs:      []string{},
					Slices:   []runmode.Slice{{Job: "4", Stage: domain.StageNeedsInfo, Rounds: 1}},
					Rounds:   1,
					Duration: time.Minute,
					Options:  runmode.Options,
				})
			})
		})

		rows := []whyRow{
			{"first in the tech lead's order", queue.Order{TechLead: []int{8, 2}, Priority: []int{2}}, 8, "es el primero en el orden del tech lead"},
			{"first in the product owner's priority", queue.Order{Priority: []int{8}}, 8, "es el primero en la prioridad del product owner"},
			{"priority ignored under a tech lead order", queue.Order{TechLead: []int{3}, Priority: []int{8}}, 8, "ningún orden lo lista y es el de número más bajo entre los que esperan"},
			{"no order", queue.Order{}, 8, "ningún orden lo lista y es el de número más bajo entre los que esperan"},
		}
		specs.Table(s, rows, func(r whyRow) string { return r.name }, func(ctx *specs.Context, r whyRow) {
			got := runmode.Summarize(alone(1, domain.StageDone), "1", []queue.Issue{{Number: r.next, Title: "next"}, {Number: 9}}, r.order)
			ctx.Expect(got.Next).ToEqual(&runmode.Next{Issue: r.next, Title: "next", Why: r.want})
		})
	})
}
