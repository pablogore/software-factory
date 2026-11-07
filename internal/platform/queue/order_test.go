package queue_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/queue"
)

type orderRow struct {
	name   string
	order  queue.Order
	issues []int
	want   []int
}

func issues(numbers ...int) []queue.Issue {
	out := make([]queue.Issue, len(numbers))
	for i, n := range numbers {
		out[i] = queue.Issue{Number: n}
	}
	return out
}

func numbers(issues []queue.Issue) []int {
	out := make([]int, len(issues))
	for i, is := range issues {
		out[i] = is.Number
	}
	return out
}

// The rows marked "main" are the order cases of `factory/bin/job queue` on
// main (read_plan and its sort, and the plan step of test_e2e.py).
func TestQueueOrder(t *testing.T) {
	specs.Describe(t, "queue.Sort", func(s *specs.Spec) {
		rows := []orderRow{
			{"no order: by issue number (main)", queue.Order{}, []int{9, 3, 5}, []int{3, 5, 9}},
			{"tech lead order wins over priority (main)", queue.Order{TechLead: []int{2, 3}, Priority: []int{3, 2}}, []int{3, 2}, []int{2, 3}},
			{"priority applies without a tech lead order (main)", queue.Order{Priority: []int{3, 2}}, []int{2, 3}, []int{3, 2}},
			{"unlisted go after the tech lead order, by number (main)", queue.Order{TechLead: []int{9, 3}}, []int{1, 3, 5, 9, 4}, []int{9, 3, 1, 4, 5}},
			{"unlisted go after the priority, by number (main)", queue.Order{Priority: []int{5, 1}}, []int{9, 1, 3, 5}, []int{5, 1, 3, 9}},
			{"priority is ignored for issues the tech lead order leaves out (main)", queue.Order{TechLead: []int{9}, Priority: []int{5, 1}}, []int{1, 5, 9}, []int{9, 1, 5}},
			{"listed issues that are not open are skipped (main)", queue.Order{TechLead: []int{7, 3, 8}}, []int{4, 3}, []int{3, 4}},
			{"an issue listed twice keeps its first place (differs from main)", queue.Order{TechLead: []int{3, 2, 3}}, []int{1, 2, 3}, []int{3, 2, 1}},
		}
		specs.Table(s, rows, func(r orderRow) string { return r.name }, func(ctx *specs.Context, r orderRow) {
			got := issues(r.issues...)
			queue.Sort(got, r.order)
			ctx.Expect(numbers(got)).ToEqual(r.want)
		})
	})
}
