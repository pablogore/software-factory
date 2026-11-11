//go:build unix

package gitdiff_test

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git's index checksum is SHA-1.
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

// s1c is a worktree whose index git diff would refresh: a.txt is racily
// clean, its entry as new as the index, so git hashes it again, and b.txt
// was touched, so its stat data is stale without a change. c.txt and d.txt
// are on the base too, for the attacks that hide a change to them.
type s1c struct {
	hostile
}

func newS1c(ctx *specs.Context) s1c {
	h := newHostile(ctx)
	h.dir, h.fork = newRepoWith(ctx, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c.txt": "old\n", "d.txt": "d\n"})
	later := time.Now().Add(time.Hour)
	ctx.Expect(os.Chtimes(filepath.Join(h.dir, "a.txt"), later, later)).To(specs.BeNil())
	// The entry takes a.txt's mtime, later than the index written now.
	must(ctx, h.dir, "update-index", "--refresh")
	ctx.Expect(os.Chtimes(filepath.Join(h.dir, "b.txt"), later, later)).To(specs.BeNil())
	return s1c{h}
}

// touch is a shell command that makes the marker, and passes its input
// through, as a clean filter must.
func (w s1c) touch() string {
	return "touch " + w.marker + "; cat"
}

// index is the real index of the worktree, byte for byte.
func (w s1c) index(ctx *specs.Context) []byte {
	data, err := os.ReadFile(filepath.Join(w.dir, ".git", "index"))
	ctx.Expect(err).To(specs.BeNil())
	return data
}

// list has the Differ list the job's changes, as the gate does.
func (w s1c) list(ctx *specs.Context) []domain.FileChange {
	changes, err := w.differ.Changes(context.Background(), job, w.dir, w.fork)
	ctx.Expect(err).To(specs.BeNil())
	return changes
}

// fakeStat changes file to content of the same size and writes its index
// entry by hand, so it keeps the base's object name with the stat data of
// the changed file: git diff trusts it and sees no change.
func (w s1c) fakeStat(ctx *specs.Context, file, content string) {
	old := must(ctx, w.dir, "rev-parse", w.fork+":"+file)
	earlier := time.Now().Add(-time.Hour)
	write(ctx, w.dir, file, content)
	ctx.Expect(os.Chtimes(filepath.Join(w.dir, file), earlier, earlier)).To(specs.BeNil())
	must(ctx, w.dir, "update-index", file)
	changed := must(ctx, w.dir, "rev-parse", ":"+file)
	data := w.index(ctx)
	from, err := hex.DecodeString(changed)
	ctx.Expect(err).To(specs.BeNil())
	to, err := hex.DecodeString(old)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(bytes.Count(data, from)).ToEqual(1)
	data = bytes.Replace(data, from, to, 1)
	sum := sha1.Sum(data[:len(data)-sha1.Size]) //nolint:gosec // git's index checksum is SHA-1.
	copy(data[len(data)-sha1.Size:], sum[:])
	ctx.Expect(os.WriteFile(filepath.Join(w.dir, ".git", "index"), data, 0o600)).To(specs.BeNil())
}

func TestDemoS1c(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ listing a hostile worktree's changes", func(s *specs.Spec) {
		s.It("runs no hook the config defines by name", func(ctx *specs.Context) {
			w := newS1c(ctx)
			must(ctx, w.dir, "config", "hook.m.command", "touch "+w.marker)
			must(ctx, w.dir, "config", "hook.m.event", "post-index-change")

			w.list(ctx)

			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("runs no hook from the folder core.hooksPath names", func(ctx *specs.Context) {
			w := newS1c(ctx)
			hooks := filepath.Join(ctx.Testing().TempDir(), "hooks")
			w.hook(ctx, hooks, "post-index-change")
			must(ctx, w.dir, "config", "core.hooksPath", hooks)

			w.list(ctx)

			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("runs no hook from .git/hooks", func(ctx *specs.Context) {
			w := newS1c(ctx)
			w.hook(ctx, filepath.Join(w.dir, ".git", "hooks"), "post-index-change")

			w.list(ctx)

			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("runs no clean filter the worktree's .gitattributes names", func(ctx *specs.Context) {
			w := newS1c(ctx)
			write(ctx, w.dir, ".gitattributes", "*.txt filter=mc\n")
			must(ctx, w.dir, "config", "filter.mc.clean", w.touch())

			w.list(ctx)

			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("runs no process filter .git/info/attributes names", func(ctx *specs.Context) {
			w := newS1c(ctx)
			write(ctx, w.dir, ".git/info/attributes", "*.txt filter=mp\n")
			must(ctx, w.dir, "config", "filter.mp.process", "touch "+w.marker)

			w.list(ctx)

			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("runs no clean filter the file core.attributesFile names", func(ctx *specs.Context) {
			w := newS1c(ctx)
			attributes := filepath.Join(ctx.Testing().TempDir(), "attributes")
			write(ctx, filepath.Dir(attributes), "attributes", "*.txt filter=mc\n")
			must(ctx, w.dir, "config", "core.attributesFile", attributes)
			must(ctx, w.dir, "config", "filter.mc.clean", w.touch())

			w.list(ctx)

			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("leaves the real index byte for byte as it was, with every attack set up at once", func(ctx *specs.Context) {
			w := newS1c(ctx)
			must(ctx, w.dir, "config", "hook.m.command", "touch "+w.marker)
			must(ctx, w.dir, "config", "hook.m.event", "post-index-change")
			w.hook(ctx, filepath.Join(w.dir, ".git", "hooks"), "post-index-change")
			write(ctx, w.dir, ".gitattributes", "a.txt filter=mc\nd.txt text\n")
			write(ctx, w.dir, ".git/info/attributes", "b.txt filter=mp\n")
			must(ctx, w.dir, "config", "filter.mc.clean", w.touch())
			must(ctx, w.dir, "config", "filter.mp.process", "touch "+w.marker)
			before := w.index(ctx)

			w.list(ctx)

			ctx.Expect(w.index(ctx)).ToEqual(before)
			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("lists a tracked file whose index entry has faked stat data", func(ctx *specs.Context) {
			w := newS1c(ctx)
			w.fakeStat(ctx, "c.txt", "new\n")
			// The premise: git diff on the real index trusts the entry.
			ctx.Expect(must(ctx, w.dir, "diff", "--name-only", w.fork, "--")).ToEqual("")

			changes := w.list(ctx)

			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "c.txt", Kind: domain.ChangeModified}})
		})

		s.It("lists a CRLF change the worktree's .gitattributes would normalise back to the base", func(ctx *specs.Context) {
			w := newS1c(ctx)
			write(ctx, w.dir, ".gitattributes", "d.txt text\n")
			must(ctx, w.dir, "add", ".gitattributes")
			must(ctx, w.dir, "commit", "-q", "-m", "attributes")
			write(ctx, w.dir, "d.txt", "d\r\n")
			// The premise: git diff on the real index normalises it away.
			// safecrlf off keeps git's warning about it out of the output.
			ctx.Expect(must(ctx, w.dir, "-c", "core.safecrlf=false", "diff", "--name-only", w.fork, "--")).ToEqual(".gitattributes")

			changes := w.list(ctx)

			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: ".gitattributes", Kind: domain.ChangeAdded},
				{Path: "d.txt", Kind: domain.ChangeModified},
			})
		})

		s.It("lists untracked files after the tracked changes", func(ctx *specs.Context) {
			w := newS1c(ctx)
			w.fakeStat(ctx, "c.txt", "new\n")
			write(ctx, w.dir, ".gitattributes", "d.txt text\n")
			write(ctx, w.dir, "d.txt", "d\r\n")
			write(ctx, w.dir, "e.txt", "e\n")

			changes := w.list(ctx)

			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "c.txt", Kind: domain.ChangeModified},
				{Path: "d.txt", Kind: domain.ChangeModified},
				{Path: ".gitattributes", Kind: domain.ChangeAdded},
				{Path: "e.txt", Kind: domain.ChangeAdded},
			})
		})

		s.It("fails, rather than list no changes, when the worktree uses a split index", func(ctx *specs.Context) {
			w := newS1c(ctx)
			write(ctx, w.dir, "c.txt", "changed\n")
			must(ctx, w.dir, "update-index", "--split-index")
			// The premise: git reads the split index fine where it is.
			ctx.Expect(must(ctx, w.dir, "diff", "--name-only", w.fork, "--")).ToEqual("c.txt")

			changes, err := w.differ.Changes(context.Background(), job, w.dir, w.fork)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(changes).To(specs.BeEmpty())
		})

		specs.Table(s, badIndexRows(), func(r badIndexRow) string { return r.name }, func(ctx *specs.Context, r badIndexRow) {
			w := newS1c(ctx)
			index := filepath.Join(w.dir, ".git", "index")
			ctx.Expect(os.Remove(index)).To(specs.BeNil())
			r.plant(ctx, index)

			// In a goroutine, so a read that blocks fails the row, not the run.
			done := make(chan error, 1)
			go func() {
				_, err := w.differ.Changes(context.Background(), job, w.dir, w.fork)
				done <- err
			}()

			select {
			case err := <-done:
				ctx.Expect(err).To(specs.Not(specs.BeNil()))
				ctx.Expect(err.Error()).To(specs.Contain(r.want))
			case <-time.After(10 * time.Second):
				ctx.Testing().Fatal("Changes did not return")
			}
		})
	})
}

// badIndexRow puts something in place of the worktree's real index that
// factoryd must refuse to read, rather than hang or run out of memory;
// want is in the error copyIndex returns, so git's own parse error doesn't
// pass for it.
type badIndexRow struct {
	name  string
	plant func(ctx *specs.Context, index string)
	want  string
}

func badIndexRows() []badIndexRow {
	return []badIndexRow{
		{"fails at once when the index is a FIFO", func(ctx *specs.Context, index string) {
			ctx.Expect(syscall.Mkfifo(index, 0o600)).To(specs.BeNil())
		}, "is not a regular file"},
		{"fails at once when the index is a symlink to /dev/zero", func(ctx *specs.Context, index string) {
			ctx.Expect(os.Symlink("/dev/zero", index)).To(specs.BeNil())
		}, "opening the index"},
		{"fails when the index is a symlink to a regular file", func(ctx *specs.Context, index string) {
			other := filepath.Join(ctx.Testing().TempDir(), "other")
			ctx.Expect(os.WriteFile(other, []byte("secret\n"), 0o600)).To(specs.BeNil())
			ctx.Expect(os.Symlink(other, index)).To(specs.BeNil())
		}, "opening the index"},
		{"fails when the index is over the size cap", func(ctx *specs.Context, index string) {
			f, err := os.Create(index)
			ctx.Expect(err).To(specs.BeNil())
			// Sparse: it takes no disk.
			ctx.Expect(f.Truncate(512<<20 + 1)).To(specs.BeNil())
			ctx.Expect(f.Close()).To(specs.BeNil())
		}, "bytes"},
	}
}
