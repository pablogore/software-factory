package queue

import (
	"cmp"
	"slices"
)

// Issue is an open issue waiting to be taken.
type Issue struct {
	Number int
	Title  string
	// Body is read for the issue's "Depends on #N" lines.
	Body string
}

// Order is the explicit order to take issues in, issue numbers first to
// last: the tech lead's, and the product owner's priority. After comes with
// it from the tech lead's plan too.
type Order struct {
	TechLead []int
	Priority []int
	// After maps an issue to those it waits for, from plan.md's
	// `AFTER: #a #b ...` lines, where #a waits for the rest. They add to the
	// issue's "Depends on #N" lines and follow the same rules (see Depends).
	After map[int][]int
}

// rank maps each listed issue to its place: the tech lead's order when it
// lists any issue, else the product owner's priority. An issue listed twice
// keeps its first place; `factory/bin/job queue` on main kept the last one,
// which could put an unlisted issue ahead of a listed one.
func (o Order) rank() map[int]int {
	list := o.TechLead
	if len(list) == 0 {
		list = o.Priority
	}
	rank := make(map[int]int, len(list))
	for _, n := range list {
		if _, ok := rank[n]; !ok {
			rank[n] = len(rank)
		}
	}
	return rank
}

// Sort puts issues in the order they are taken: the listed ones in o's
// order, then the unlisted ones, ties by issue number.
func Sort(issues []Issue, o Order) {
	rank := o.rank()
	place := func(n int) int {
		if r, ok := rank[n]; ok {
			return r
		}
		return len(rank)
	}
	slices.SortFunc(issues, func(a, b Issue) int {
		return cmp.Or(cmp.Compare(place(a.Number), place(b.Number)), cmp.Compare(a.Number, b.Number))
	})
}
