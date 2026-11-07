// Package build is the build slice's deterministic gate: once a builder's
// work is READY, Gate runs the repo's checks in the job's worktree, with no
// agent, judges the job's diff against its base by rules such as
// ProtectedPaths, and hands Decide the verdict with the run and the rules the
// diff broke. Decide keeps the run with the job as a ChecksRan, so a failing
// gate's log and reasons reach the builder of the next round (LastChecks) and
// no reviewer starts on a broken branch.
//
// The checks command and the protected paths are the factory's own
// configuration, never read from the job's branch, and a diff that modifies
// or deletes a protected path fails the gate, so a builder cannot change what
// it is judged by.
package build
