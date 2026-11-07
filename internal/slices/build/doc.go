// Package build is the build slice's deterministic gate: once a builder's
// work is READY, Gate runs the repo's checks in the job's worktree, with no
// agent, judges the job's diff against its base by rules such as
// ProtectedPaths and the build role's AllowedPaths, and hands Decide the
// verdict with the run and the rules the diff broke. Decide keeps the run
// with the job as a ChecksRan, so a failing gate's log and reasons reach the
// builder of the next round (LastChecks) and no reviewer starts on a broken
// branch.
//
// The checks command and the protected paths are the factory's own
// configuration, never read from the job's branch, and a diff that modifies
// or deletes a protected path, or adds a makefile variant or a file under
// .github/, fails the gate, so a builder cannot change what it is judged by.
// So does an index that marks a file skip-worktree or assume-unchanged,
// since it hides that file's changes from the diff. A diff that deletes or
// moves a test file, or adds .skip, .only or t.Skip to one, fails it too, so
// checks never pass only because tests stopped running. With a test count
// command, a package with fewer tests in the job's head than in a copy of
// its base commit fails it as well, however the tests went: with go test
// -json, a subtest deleted or skipped, such as a go-specs It or table row,
// is one fewer. With none, the run notes that the rule did not run.
package build
