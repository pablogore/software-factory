// Package build is the build slice's deterministic gate: once a builder's
// work is READY, Gate runs the repo's checks in the job's worktree, with no
// agent, and hands Decide the verdict with the run. Decide keeps the run with
// the job as a ChecksRan, so a failing gate's log reaches the builder of the
// next round (LastChecks) and no reviewer starts on a broken branch.
//
// The checks command is the factory's own configuration, never read from the
// job's branch, so a builder cannot change what it is judged by.
package build
