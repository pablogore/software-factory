package queue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/queue"
)

var errUnreachable = errors.New("github: 502 bad gateway")

// noDeps is a fake queue.Issues for queues whose issues depend on none.
type noDeps struct{}

func (noDeps) Finished(_ context.Context, n int) (bool, error) {
	return false, fmt.Errorf("issue #%d: %w", n, queue.ErrNoIssue)
}

// tracker is a fake queue.Issues: the issues it knows are open until
// finished, the rest do not exist, and those it is told to fail cannot be
// read until they recover.
type tracker struct {
	mu      sync.Mutex
	done    map[int]bool
	failing map[int]bool
}

func newTracker(open ...int) *tracker {
	t := &tracker{done: map[int]bool{}, failing: map[int]bool{}}
	for _, n := range open {
		t.done[n] = false
	}
	return t
}

func (t *tracker) Finished(_ context.Context, n int) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failing[n] {
		return false, fmt.Errorf("issue #%d: %w", n, errUnreachable)
	}
	done, ok := t.done[n]
	if !ok {
		return false, fmt.Errorf("issue #%d: %w", n, queue.ErrNoIssue)
	}
	return done, nil
}

// fail makes reading issue n fail for now, or work again when failing is
// false.
func (t *tracker) fail(n int, failing bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failing[n] = failing
}

// finish marks issue n merged or closed.
func (t *tracker) finish(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done[n] = true
}

func dependent(n int, body string) queue.Issue { return queue.Issue{Number: n, Body: body} }

type dependsRow struct {
	name string
	body string
	want []int
}

func TestDepends(t *testing.T) {
	specs.Describe(t, "queue.Depends", func(s *specs.Spec) {
		rows := []dependsRow{
			{"no body", "", nil},
			{"no Depends on line", "Fixes #4.\nSee #5.", nil},
			{"one line per dependency", "Part of epic #13.\nDepends on #32.\nDepends on #26.", []int{32, 26}},
			{"several on one line", "Depends on #1, #2 and #3", []int{1, 2, 3}},
			{"any case, quoted or listed", "> depends on #7\n- DEPENDS ON #8", []int{7, 8}},
			{"repeats once", "Depends on #2\nDepends on #2, #1", []int{2, 1}},
			{"only at the start of a line", "This no longer depends on #9.", nil},
		}
		specs.Table(s, rows, func(r dependsRow) string { return r.name }, func(ctx *specs.Context, r dependsRow) {
			ctx.Expect(queue.Depends(r.body)).ToEqual(r.want)
		})
	})
}

func TestQueueDependencies(t *testing.T) {
	specs.Describe(t, "queue.Queue with dependencies", func(s *specs.Spec) {
		s.When("an issue depends on an open one", func(s *specs.Spec) {
			s.It("is not taken although there are free slots", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 3, costs{}), queue.Order{}, newTracker(1), r)
				q.Add(context.Background(), dependent(2, "Depends on #1"))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{2})
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(2, "Depends on #1"), Reason: "espera #1"}})
			})

			s.It("lets the next ready one in the order go ahead", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{}, newTracker(1), r)
				q.Add(context.Background(), dependent(2, "Depends on #1"), dependent(3, ""))
				r.awaitStart(ctx, 3)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{2})
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(2, "Depends on #1"), Reason: "espera #1"}})
				ctx.Expect(r.end(ctx, 3, nil)).To(specs.BeNil())
				ctx.Expect(r.started()).ToEqual([]int{3})
			})
		})

		s.When("an issue has several dependencies", func(s *specs.Spec) {
			s.It("names each one missing, unknown ones too", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(1, 4, 5)
				deps.finish(4)
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{}, deps, r)
				body := "Depends on #1\nDepends on #4\nDepends on #7, #5"
				q.Add(context.Background(), dependent(2, body))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(2, body), Reason: "espera #1, dependencia desconocida #7, #5"}})
			})
		})

		s.When("an issue depends on one that does not exist", func(s *specs.Spec) {
			s.It("waits and is shown as an unknown dependency", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 2, costs{}), queue.Order{}, newTracker(), r)
				q.Add(context.Background(), dependent(2, "Depends on #404"))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{2})
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(2, "Depends on #404"), Reason: "espera dependencia desconocida #404"}})
			})
		})

		s.When("a dependency cannot be read for now", func(s *specs.Spec) {
			s.It("waits without calling it unknown and reads it again at the next poll", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(1)
				deps.fail(1, true)
				q := queue.New(newScheduler(ctx, 2, costs{}), queue.Order{}, deps, r)
				q.Add(context.Background(), dependent(2, "Depends on #1"))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(2, "Depends on #1"), Reason: "espera #1"}})
				deps.fail(1, false)
				deps.finish(1)
				q.Add(context.Background(), dependent(2, "Depends on #1"))
				r.awaitStart(ctx, 2)
				ctx.Expect(q.Held()).To(specs.BeEmpty())
				ctx.Expect(r.end(ctx, 2, nil)).To(specs.BeNil())
			})
		})

		s.When("the dependencies finish", func(s *specs.Spec) {
			s.It("takes the issue once the one it depends on from the queue is merged", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(1)
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{}, deps, r)
				q.Add(context.Background(), dependent(1, ""), dependent(2, "Depends on #1"), dependent(3, ""))
				r.awaitStart(ctx, 1)
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(2, "Depends on #1"), Reason: "espera #1"}})
				deps.finish(1)
				ctx.Expect(r.end(ctx, 1, nil)).To(specs.BeNil())
				r.awaitStart(ctx, 3)
				ctx.Expect(q.Held()).To(specs.BeEmpty())
				ctx.Expect(r.end(ctx, 3, nil)).To(specs.BeNil())
				r.awaitStart(ctx, 2)
				ctx.Expect(r.end(ctx, 2, nil)).To(specs.BeNil())
				ctx.Expect(r.started()).ToEqual([]int{1, 3, 2})
			})

			s.It("takes the issue at the next poll once one outside the queue is closed", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(1)
				q := queue.New(newScheduler(ctx, 2, costs{}), queue.Order{}, deps, r)
				q.Add(context.Background(), dependent(2, "Depends on #1"))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				deps.finish(1)
				q.Add(context.Background(), dependent(2, "Depends on #1"))
				r.awaitStart(ctx, 2)
				ctx.Expect(q.Held()).To(specs.BeEmpty())
				ctx.Expect(r.end(ctx, 2, nil)).To(specs.BeNil())
			})
		})
	})
}

// after is the order of a plan with only AFTER lines, `AFTER: #n #deps...`
// for each entry.
func after(lines map[int][]int) queue.Order { return queue.Order{After: lines} }

// The cases marked "main" are those of `factory/bin/job queue` on main for
// plan.md's AFTER lines (cmd_queue's hold rows and the plan step of
// test_e2e.py).
func TestQueueAfter(t *testing.T) {
	specs.Describe(t, "queue.Queue with the tech lead's AFTER lines", func(s *specs.Spec) {
		s.When("an issue's AFTER names an open one", func(s *specs.Spec) {
			s.It("is not taken and waits for it, from plan.md (main)", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 3, costs{}), after(map[int][]int{3: {2}}), newTracker(2), r)
				q.Add(context.Background(), dependent(3, ""))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{3})
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, ""), Reason: "espera #2 (plan.md)"}})
			})

			s.It("holds only the first issue of the line, not the ones it waits for (main)", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), after(map[int][]int{3: {2}}), newTracker(2), r)
				q.Add(context.Background(), dependent(2, ""), dependent(3, ""))
				r.awaitStart(ctx, 2)
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, ""), Reason: "espera #2 (plan.md)"}})
				ctx.Expect(r.end(ctx, 2, nil)).To(specs.BeNil())
				ctx.Expect(r.started()).ToEqual([]int{2})
			})

			s.It("names each one still open, in the line's order (main sorts them)", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(5, 1, 4)
				deps.finish(1)
				q := queue.New(newScheduler(ctx, 1, costs{}), after(map[int][]int{9: {5, 1, 4}}), deps, r)
				q.Add(context.Background(), dependent(9, ""))
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(9, ""), Reason: "espera #5 (plan.md), #4 (plan.md)"}})
			})
		})

		s.When("an issue has dependencies in its body and in plan.md", func(s *specs.Spec) {
			s.It("waits for all of them, and names the plan's ones as such", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), after(map[int][]int{3: {2, 1}}), newTracker(1, 2), r)
				q.Add(context.Background(), dependent(3, "Depends on #1"))
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, "Depends on #1"), Reason: "espera #1, #2 (plan.md)"}})
			})

			s.It("is taken only once those of both are merged or closed", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(1, 2)
				q := queue.New(newScheduler(ctx, 1, costs{}), after(map[int][]int{3: {2}}), deps, r)
				q.Add(context.Background(), dependent(3, "Depends on #1"))
				deps.finish(1)
				q.Add(context.Background())
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, "Depends on #1"), Reason: "espera #2 (plan.md)"}})
				deps.finish(2)
				q.Add(context.Background())
				r.awaitStart(ctx, 3)
				ctx.Expect(q.Held()).To(specs.BeEmpty())
				ctx.Expect(r.end(ctx, 3, nil)).To(specs.BeNil())
			})

			s.It("names a dependency both list once, as the body's", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), after(map[int][]int{3: {1}}), newTracker(1), r)
				q.Add(context.Background(), dependent(3, "Depends on #1"))
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, "Depends on #1"), Reason: "espera #1"}})
			})
		})

		s.When("an issue's AFTER names one that does not exist", func(s *specs.Spec) {
			s.It("waits and is shown as an unknown dependency from plan.md (main went ahead)", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 2, costs{}), after(map[int][]int{3: {404}}), newTracker(), r)
				q.Add(context.Background(), dependent(3, ""))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, ""), Reason: "espera dependencia desconocida #404 (plan.md)"}})
			})
		})

		s.When("an issue's AFTER names one that cannot be read for now", func(s *specs.Spec) {
			s.It("waits without calling it unknown and reads it again at the next poll", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(2)
				deps.fail(2, true)
				q := queue.New(newScheduler(ctx, 2, costs{}), after(map[int][]int{3: {2}}), deps, r)
				q.Add(context.Background(), dependent(3, ""))
				ctx.Expect(r.started()).To(specs.BeEmpty())
				ctx.Expect(q.Held()).ToEqual([]queue.Hold{{Issue: dependent(3, ""), Reason: "espera #2 (plan.md)"}})
				deps.fail(2, false)
				deps.finish(2)
				q.Add(context.Background())
				r.awaitStart(ctx, 3)
				ctx.Expect(q.Held()).To(specs.BeEmpty())
				ctx.Expect(r.end(ctx, 3, nil)).To(specs.BeNil())
			})
		})

		s.When("the issue an AFTER names is merged (main)", func(s *specs.Spec) {
			s.It("takes the one waiting for it next, although the order put it first", func(ctx *specs.Context) {
				r := newRunner()
				deps := newTracker(2)
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{TechLead: []int{3, 2}, After: map[int][]int{3: {2}}}, deps, r)
				q.Add(context.Background(), dependent(2, ""), dependent(3, ""))
				r.awaitStart(ctx, 2)
				deps.finish(2)
				ctx.Expect(r.end(ctx, 2, nil)).To(specs.BeNil())
				r.awaitStart(ctx, 3)
				ctx.Expect(r.end(ctx, 3, nil)).To(specs.BeNil())
				ctx.Expect(r.started()).ToEqual([]int{2, 3})
			})
		})
	})
}
