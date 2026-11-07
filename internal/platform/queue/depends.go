package queue

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Issues tells the queue where the issues others depend on stand.
type Issues interface {
	// Finished reports whether issue n is merged or closed. It returns an
	// error wrapping ErrNoIssue when n does not exist, and any other error
	// when n cannot be read for now. The queue asks while it holds its
	// lock, so a slow answer holds every look for room back.
	Finished(ctx context.Context, n int) (bool, error)
}

// ErrNoIssue is what Issues.Finished wraps when the issue does not exist.
var ErrNoIssue = errors.New("issue does not exist")

var (
	dependsLine = regexp.MustCompile(`(?im)^[ \t>*-]*depends on\b(.*)$`)
	issueRef    = regexp.MustCompile(`#(\d+)`)
)

// Depends returns the issues body depends on, from its "Depends on #N"
// lines, without repeats and in the order they appear. `factory/bin/job
// queue` on main read them from plan.md's AFTER lines instead.
func Depends(body string) []int {
	var deps []int
	for _, line := range dependsLine.FindAllStringSubmatch(body, -1) {
		for _, ref := range issueRef.FindAllStringSubmatch(line[1], -1) {
			n, err := strconv.Atoi(ref[1])
			if err == nil && !slices.Contains(deps, n) {
				deps = append(deps, n)
			}
		}
	}
	return deps
}

// hold returns why is must wait, naming each dependency that is not merged
// or closed, or "" when is can be taken. Any open dependency holds it, while
// main only waited on open `factory` issues and unfinished jobs; one that
// does not exist holds it too and is named unknown, where main went ahead.
// One that cannot be read for now holds it as if open, and is read again at
// the next look for room.
func hold(ctx context.Context, deps Issues, is Issue) string {
	var missing []string
	for _, n := range Depends(is.Body) {
		finished, err := deps.Finished(ctx, n)
		switch {
		case errors.Is(err, ErrNoIssue):
			missing = append(missing, fmt.Sprintf("dependencia desconocida #%d", n))
		case err != nil, !finished:
			missing = append(missing, fmt.Sprintf("#%d", n))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "espera " + strings.Join(missing, ", ")
}
