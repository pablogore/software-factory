package runmode_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/runmode"
	"github.com/pablogore/software-factory/internal/ports"
)

// stops is a fake runmode.StopInbox.
type stops struct {
	mu      sync.Mutex
	entries map[string]runmode.Stop
}

func (i *stops) PutStop(_ context.Context, key string, s runmode.Stop) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.entries == nil {
		i.entries = map[string]runmode.Stop{}
	}
	i.entries[key] = s
	return nil
}

func (i *stops) all() map[string]runmode.Stop {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := map[string]runmode.Stop{}
	for k, v := range i.entries {
		out[k] = v
	}
	return out
}

// sliced is issue n worked as one slice job per cost in usd, from the
// origin thread issue/n.
func sliced(n int, usd ...float64) runmode.IssueReport {
	origin := domain.Origin{Provider: "github", Thread: fmt.Sprintf("issue/%d", n), Author: "ana"}
	r := runmode.IssueReport{Issue: queue.Issue{Number: n, Title: fmt.Sprintf("Issue %d", n)}}
	for i, c := range usd {
		id := domain.JobID(fmt.Sprintf("%d-s%d", n, i+1))
		r.Jobs = append(r.Jobs, runmode.JobReport{Job: domain.Job{ID: id, Stage: domain.StageDone, Initiative: domain.InitiativeID(fmt.Sprint(n)), Origin: origin}, USD: c})
	}
	return r
}

// stopWords is an example channel's wording of every cap, by language. The
// real channels come with E07; each words the codes its own way.
var stopWords = map[string]map[domain.IssueCap]string{
	"es": {
		domain.CapSlices: "el issue llegó a %v slices, el tope es %v",
		domain.CapUSD:    "el issue gastó USD %v, el tope es USD %v",
	},
	"en": {
		domain.CapSlices: "the issue reached %v slices, the cap is %v",
		domain.CapUSD:    "the issue spent USD %v, the cap is USD %v",
	},
}

func await(ctx *specs.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(patience):
		ctx.Errorf("the work did not finish")
		return nil
	}
}

func TestStarter(t *testing.T) {
	specs.Describe(t, "runmode.Starter", func(s *specs.Spec) {
		s.When("one issue has 5 slices and another has 1", func(s *specs.Spec) {
			s.It("starts no sixth slice, lets the running job end, goes on with the other issue and tells why", func(ctx *specs.Context) {
				bg := context.Background()
				sched := newScheduler(ctx, 3, costs{})
				out, in := &outbox{}, &stops{}
				st := runmode.NewStarter(sched, runmode.Caps{}, reports{}.add(sliced(1, 1, 1, 1, 1, 1)).add(sliced(2, 1)), out, in)

				// A job of issue #1 already runs when its replan asks for
				// another slice.
				release := make(chan struct{})
				running := sched.Submit(bg, "1-s5", func(context.Context) error { <-release; return nil })

				var sixth atomic.Bool
				done, err := st.Start(bg, domain.NextSliceRequested{Job: "1-s5", Initiative: "1", At: epoch}, "1-s6", func(context.Context) error {
					sixth.Store(true)
					return nil
				})
				ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
				ctx.Expect(done).To(specs.BeNil())

				other, err := st.Start(bg, domain.NextSliceRequested{Job: "2-s1", Initiative: "2", At: epoch}, "2-s2", func(context.Context) error { return nil })
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, other)).To(specs.BeNil())

				close(release)
				ctx.Expect(await(ctx, running)).To(specs.BeNil())
				ctx.Expect(sixth.Load()).To(specs.BeFalse())

				want := runmode.Stop{Issue: 1, Title: "Issue 1", Cap: domain.CapSlices, Limit: 5, Reached: 5}
				acts := out.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Job).ToEqual(domain.JobID("1-s5"))
				ctx.Expect(acts[0].Kind).ToEqual(runmode.ActionCapped)
				var posted struct {
					Origin domain.Origin `json:"origin"`
					Stop   runmode.Stop  `json:"stop"`
				}
				ctx.Expect(json.Unmarshal([]byte(acts[0].Payload), &posted)).To(specs.BeNil())
				ctx.Expect(posted.Origin.Thread).ToEqual("issue/1")
				ctx.Expect(posted.Stop).ToEqual(want)
				ctx.Expect(acts[0].Payload).To(specs.Contain(`"cap":"issue-slices","limit":5,"reached":5`))
				ctx.Expect(in.all()).ToEqual(map[string]runmode.Stop{acts[0].Key: want})
			})
		})

		s.When("an issue's jobs cost more than USD 8", func(s *specs.Spec) {
			s.It("starts no new slice and tells the cap and the cost reached", func(ctx *specs.Context) {
				out, in := &outbox{}, &stops{}
				st := runmode.NewStarter(newScheduler(ctx, 1, costs{}), runmode.Caps{}, reports{}.add(sliced(3, 5, 3.5)), out, in)
				_, err := st.Start(context.Background(), domain.NextSliceRequested{Job: "3-s2", Initiative: "3", At: epoch}, "3-s3", func(context.Context) error {
					ctx.Errorf("the slice started")
					return nil
				})
				ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
				ctx.Expect(out.saved()[0].Payload).To(specs.Contain(`"cap":"issue-usd","limit":8,"reached":8.5`))
				ctx.Expect(in.all()[out.saved()[0].Key]).ToEqual(runmode.Stop{Issue: 3, Title: "Issue 3", Cap: domain.CapUSD, Limit: 8, Reached: 8.5})
			})
		})

		s.When("the run's config sets the caps", func(s *specs.Spec) {
			s.It("stops at them instead of the defaults", func(ctx *specs.Context) {
				bg := context.Background()
				reps := reports{}.add(sliced(1, 1, 1)).add(sliced(2, 0.5, 9))
				out := &outbox{}
				st := runmode.NewStarter(newScheduler(ctx, 1, costs{}), runmode.Caps{Slices: 2, USD: 20}, reps, out, &stops{})

				_, err := st.Start(bg, domain.NextSliceRequested{Job: "1-s2", Initiative: "1", At: epoch}, "1-s3", func(context.Context) error { return nil })
				ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
				ctx.Expect(out.saved()[0].Payload).To(specs.Contain(`"cap":"issue-slices","limit":2,"reached":2`))

				st = runmode.NewStarter(newScheduler(ctx, 1, costs{}), runmode.Caps{Slices: 10, USD: 20}, reps, &outbox{}, &stops{})
				done, err := st.Start(bg, domain.NextSliceRequested{Job: "2-s2", Initiative: "2", At: epoch}, "2-s3", func(context.Context) error { return nil })
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, done)).To(specs.BeNil())
			})
		})

		s.When("the stop is published again", func(s *specs.Spec) {
			s.It("keeps it once", func(ctx *specs.Context) {
				out, in := &outbox{}, &stops{}
				st := runmode.NewStarter(newScheduler(ctx, 1, costs{}), runmode.Caps{}, reports{}.add(sliced(1, 1, 1, 1, 1, 1)), out, in)
				ev := domain.NextSliceRequested{Job: "1-s5", Initiative: "1", At: epoch}
				for range 2 {
					_, err := st.Start(context.Background(), ev, "1-s6", func(context.Context) error { return nil })
					ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
				}
				ctx.Expect(len(out.saved())).ToEqual(1)
				ctx.Expect(len(in.all())).ToEqual(1)
			})
		})

		s.When("the report of the job cannot be read", func(s *specs.Spec) {
			s.It("starts nothing and returns the error", func(ctx *specs.Context) {
				st := runmode.NewStarter(newScheduler(ctx, 1, costs{}), runmode.Caps{}, reports{}, &outbox{}, &stops{})
				_, err := st.Start(context.Background(), domain.NextSliceRequested{Job: "9", Initiative: "9", At: epoch}, "9-s2", func(context.Context) error {
					ctx.Errorf("the slice started")
					return nil
				})
				ctx.Expect(errors.Is(err, ports.ErrJobNotFound)).To(specs.BeTrue())
			})
		})

		s.When("a channel words the stop", func(s *specs.Spec) {
			s.It("has words for every cap in every language", func(ctx *specs.Context) {
				for _, lang := range []string{"es", "en"} {
					for _, c := range domain.IssueCaps() {
						ctx.Expect(stopWords[lang][c]).To(specs.Not(specs.BeEmpty()))
					}
				}
				ctx.Expect(fmt.Sprintf(stopWords["es"][domain.CapUSD], 8.5, 8.0)).ToEqual("el issue gastó USD 8.5, el tope es USD 8")
			})
		})
	})
}

type cappedRow struct {
	name   string
	report runmode.IssueReport
	want   runmode.Stop
	capped bool
}

func TestCapped(t *testing.T) {
	specs.Describe(t, "runmode.Capped", func(s *specs.Spec) {
		rows := []cappedRow{
			{"under both caps", sliced(1, 1, 1, 1, 1), runmode.Stop{}, false},
			{"at USD 8 exactly", sliced(1, 4, 4), runmode.Stop{}, false},
			{"at 5 slices", sliced(1, 0, 0, 0, 0, 0), runmode.Stop{Issue: 1, Title: "Issue 1", Cap: domain.CapSlices, Limit: 5, Reached: 5}, true},
			{"past USD 8", sliced(1, 4, 4.25), runmode.Stop{Issue: 1, Title: "Issue 1", Cap: domain.CapUSD, Limit: 8, Reached: 8.25}, true},
			{"past both, slices first", sliced(1, 2, 2, 2, 2, 2), runmode.Stop{Issue: 1, Title: "Issue 1", Cap: domain.CapSlices, Limit: 5, Reached: 5}, true},
		}
		specs.Table(s, rows, func(r cappedRow) string { return r.name }, func(ctx *specs.Context, r cappedRow) {
			got, capped := runmode.Capped(r.report, runmode.DefaultCaps)
			ctx.Expect(capped).ToEqual(r.capped)
			ctx.Expect(got).ToEqual(r.want)
		})
	})
}
