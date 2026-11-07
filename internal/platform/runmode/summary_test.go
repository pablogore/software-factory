package runmode_test

import (
	"fmt"
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
	want  domain.NextReason
}

// words is an example channel's wording of every reason, by language. The
// real channels come with E07; each words the codes its own way.
var words = map[string]map[domain.NextReason]string{
	"es": {
		domain.NextTechLead: "es el primero en el orden del tech lead",
		domain.NextPriority: "es el primero en la prioridad del product owner",
		domain.NextLowest:   "ningún orden lo lista y es el de número más bajo entre los que esperan",
		domain.NoNextEmpty:  "no queda ningún issue en la cola",
		domain.NoNextHeld:   "todos los issues que esperan están retenidos hasta que se cierre aquello de lo que dependen",
	},
	"en": {
		domain.NextTechLead: "it is first in the tech lead's order",
		domain.NextPriority: "it is first in the product owner's priority",
		domain.NextLowest:   "no order lists it and it is the lowest number waiting",
		domain.NoNextEmpty:  "no issue is left in the queue",
		domain.NoNextHeld:   "every waiting issue is held by its dependencies",
	},
}

// recommend is how the example channel shows a summary's next issue in
// language.
func recommend(s runmode.Summary, language string) string {
	if s.Next == nil {
		return words[language][s.NoNext]
	}
	return fmt.Sprintf("#%d %s: %s", s.Next.Issue, s.Next.Title, words[language][s.Next.Why])
}

type wordsRow struct {
	language string
	waiting  []queue.Issue
	held     []queue.Hold
	want     string
}

func TestSummarize(t *testing.T) {
	specs.Describe(t, "runmode.Summarize", func(s *specs.Spec) {
		s.When("an issue was a job on its own", func(s *specs.Spec) {
			s.It("sums it up with no PR and no next issue when none waits", func(ctx *specs.Context) {
				r := alone(4, domain.StageNeedsInfo)
				got := runmode.Summarize(r, "4", nil, nil, queue.Order{})
				ctx.Expect(got).ToEqual(runmode.Summary{
					Issue: 4, Title: "Issue 4", Stage: domain.StageNeedsInfo,
					PRs:      []string{},
					Slices:   []runmode.Slice{{Job: "4", Stage: domain.StageNeedsInfo, Rounds: 1}},
					Rounds:   1,
					Duration: time.Minute,
					NoNext:   domain.NoNextEmpty,
					Options:  runmode.Options,
				})
			})
		})

		rows := []whyRow{
			{"first in the tech lead's order", queue.Order{TechLead: []int{8, 2}, Priority: []int{2}}, 8, domain.NextTechLead},
			{"first in the product owner's priority", queue.Order{Priority: []int{8}}, 8, domain.NextPriority},
			{"priority ignored under a tech lead order", queue.Order{TechLead: []int{3}, Priority: []int{8}}, 8, domain.NextLowest},
			{"no order", queue.Order{}, 8, domain.NextLowest},
		}
		specs.Table(s, rows, func(r whyRow) string { return r.name }, func(ctx *specs.Context, r whyRow) {
			got := runmode.Summarize(alone(1, domain.StageDone), "1", []queue.Issue{{Number: r.next, Title: "next"}, {Number: 9}}, nil, r.order)
			ctx.Expect(got.Next).ToEqual(&runmode.Next{Issue: r.next, Title: "next", Why: r.want})
			ctx.Expect(got.NoNext).ToEqual(domain.NextReason(""))
		})

		s.When("the queue holds the first issues waiting", func(s *specs.Spec) {
			s.It("recommends the first one it does not hold, by its own reason", func(ctx *specs.Context) {
				waiting := []queue.Issue{{Number: 2, Title: "held"}, {Number: 5, Title: "held too"}, {Number: 7, Title: "free"}}
				held := []queue.Hold{{Issue: waiting[0], Reason: "espera #50"}, {Issue: waiting[1], Reason: "espera #51"}}
				got := runmode.Summarize(alone(1, domain.StageDone), "1", waiting, held, queue.Order{TechLead: []int{2, 5}})
				ctx.Expect(got.Next).ToEqual(&runmode.Next{Issue: 7, Title: "free", Why: domain.NextLowest})
			})

			s.It("recommends none and says they are all held when it holds every one", func(ctx *specs.Context) {
				waiting := []queue.Issue{{Number: 2}, {Number: 5}}
				held := []queue.Hold{{Issue: waiting[0], Reason: "espera #50"}, {Issue: waiting[1], Reason: "espera #50"}}
				got := runmode.Summarize(alone(1, domain.StageDone), "1", waiting, held, queue.Order{})
				ctx.Expect(got.Next).To(specs.BeNil())
				ctx.Expect(got.NoNext).ToEqual(domain.NoNextHeld)
			})
		})

		s.When("a channel shows the summary", func(s *specs.Spec) {
			s.It("has a wording for every reason in every language", func(ctx *specs.Context) {
				for _, catalog := range words {
					ctx.Expect(catalog).To(specs.HaveLen(len(domain.NextReasons())))
					for _, r := range domain.NextReasons() {
						ctx.Expect(catalog).To(specs.HaveKey(r))
					}
				}
			})

			free := []queue.Issue{{Number: 7, Title: "Run mode"}}
			held := []queue.Hold{{Issue: queue.Issue{Number: 7}, Reason: "espera #50"}}
			rows := []wordsRow{
				{"es", free, nil, "#7 Run mode: es el primero en el orden del tech lead"},
				{"en", free, nil, "#7 Run mode: it is first in the tech lead's order"},
				{"es", free, held, "todos los issues que esperan están retenidos hasta que se cierre aquello de lo que dependen"},
				{"en", free, held, "every waiting issue is held by its dependencies"},
				{"es", nil, nil, "no queda ningún issue en la cola"},
				{"en", nil, nil, "no issue is left in the queue"},
			}
			name := func(r wordsRow) string {
				return fmt.Sprintf("words %d waiting, %d held in %s", len(r.waiting), len(r.held), r.language)
			}
			specs.Table(s, rows, name, func(ctx *specs.Context, r wordsRow) {
				got := runmode.Summarize(alone(1, domain.StageDone), "1", r.waiting, r.held, queue.Order{TechLead: []int{7}})
				ctx.Expect(recommend(got, r.language)).ToEqual(r.want)
			})
		})
	})
}
