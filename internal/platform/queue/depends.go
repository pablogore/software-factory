package queue

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Issues tells the queue where the issues others depend on stand.
type Issues interface {
	// Finished reports whether issue n is merged or closed. It returns an
	// error when n does not exist or cannot be read. The queue asks while
	// it holds its lock, so a slow answer holds every look for room back.
	Finished(ctx context.Context, n int) (bool, error)
}

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
// does not exist or cannot be read holds it too, where main went ahead.
func hold(ctx context.Context, deps Issues, is Issue) string {
	var missing []string
	for _, n := range Depends(is.Body) {
		finished, err := deps.Finished(ctx, n)
		switch {
		case err != nil:
			missing = append(missing, fmt.Sprintf("#%d (dependencia desconocida)", n))
		case !finished:
			missing = append(missing, fmt.Sprintf("#%d", n))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "espera " + strings.Join(missing, ", ")
}
