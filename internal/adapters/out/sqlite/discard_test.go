package sqlite_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/ports"
)

func TestDiscardLog(t *testing.T) {
	specs.Describe(t, "sqlite.Store discard log", func(s *specs.Spec) {
		discardContract(s, func(ctx *specs.Context) ports.DiscardLog { return openStore(ctx) })
	})
	specs.Describe(t, "memstore.Store (fake) discard log", func(s *specs.Spec) {
		discardContract(s, func(ctx *specs.Context) ports.DiscardLog { return newMemstore(ctx) })
	})
}

func discarded(request, author string, minute int) ports.Discarded {
	return ports.Discarded{Provider: "telegram", Thread: "chat-1", Request: request, Author: author, At: at(minute)}
}

// discardContract is how every store keeps discarded requests: the SQLite
// store and the in-memory fake run it alike.
func discardContract(s *specs.Spec, newLog func(ctx *specs.Context) ports.DiscardLog) {
	bg := context.Background()

	s.It("lists nothing before a request is discarded", func(ctx *specs.Context) {
		got, err := newLog(ctx).Discards(bg)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.BeEmpty())
	})

	s.It("lists discarded requests in the order they were saved", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.Discard(bg, discarded("2", "mallory", 1))).To(specs.BeNil())
		ctx.Expect(log.Discard(bg, discarded("1", "eve", 2))).To(specs.BeNil())

		got, err := log.Discards(bg)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual([]ports.Discarded{discarded("2", "mallory", 1), discarded("1", "eve", 2)})
	})

	s.It("keeps the first of the same request discarded twice, without error", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.Discard(bg, discarded("1", "mallory", 1))).To(specs.BeNil())

		ctx.Expect(log.Discard(bg, discarded("1", "mallory", 5))).To(specs.BeNil())

		got, err := log.Discards(bg)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual([]ports.Discarded{discarded("1", "mallory", 1)})
	})

	s.It("keeps requests apart by provider and thread", func(ctx *specs.Context) {
		log := newLog(ctx)
		other := discarded("1", "mallory", 2)
		other.Thread = "chat-2"
		elsewhere := discarded("1", "mallory", 3)
		elsewhere.Provider = "discord"
		ctx.Expect(log.Discard(bg, discarded("1", "mallory", 1))).To(specs.BeNil())
		ctx.Expect(log.Discard(bg, other)).To(specs.BeNil())
		ctx.Expect(log.Discard(bg, elsewhere)).To(specs.BeNil())

		got, err := log.Discards(bg)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual([]ports.Discarded{discarded("1", "mallory", 1), other, elsewhere})
	})

	s.It("keeps at most ports.MaxDiscardField bytes of each field, as valid UTF-8", func(ctx *specs.Context) {
		log := newLog(ctx)
		huge := "x" + strings.Repeat("é", ports.MaxDiscardField)
		ctx.Expect(log.Discard(bg, ports.Discarded{Provider: huge, Thread: huge, Request: huge, Author: huge, At: at(1)})).To(specs.BeNil())

		got, err := log.Discards(bg)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.HaveLen(1))
		for _, f := range []string{got[0].Provider, got[0].Thread, got[0].Request, got[0].Author} {
			ctx.Expect(len(f) <= ports.MaxDiscardField).To(specs.BeTrue())
			ctx.Expect(len(f) >= ports.MaxDiscardField-utf8.UTFMax).To(specs.BeTrue())
			ctx.Expect(utf8.ValidString(f)).To(specs.BeTrue())
			ctx.Expect(strings.HasPrefix(huge, f)).To(specs.BeTrue())
		}
	})

	s.It("tells a repeat of a huge request by what it keeps of it", func(ctx *specs.Context) {
		log := newLog(ctx)
		huge := strings.Repeat("1", 10*ports.MaxDiscardField)
		ctx.Expect(log.Discard(bg, discarded(huge, "mallory", 1))).To(specs.BeNil())

		ctx.Expect(log.Discard(bg, discarded(huge, "mallory", 2))).To(specs.BeNil())

		got, err := log.Discards(bg)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual([]ports.Discarded{discarded(huge[:ports.MaxDiscardField], "mallory", 1)})
	})

	s.It("keeps the newest ports.MaxDiscards requests, dropping the oldest", func(ctx *specs.Context) {
		log := newLog(ctx)
		const extra = 3
		for i := range ports.MaxDiscards + extra {
			ctx.Expect(log.Discard(bg, discarded(strconv.Itoa(i), "mallory", i))).To(specs.BeNil())
		}

		got, err := log.Discards(bg)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.HaveLen(ports.MaxDiscards))
		ctx.Expect(got[0]).ToEqual(discarded(strconv.Itoa(extra), "mallory", extra))
		ctx.Expect(got[len(got)-1]).ToEqual(discarded(strconv.Itoa(ports.MaxDiscards+extra-1), "mallory", ports.MaxDiscards+extra-1))
	})
}
