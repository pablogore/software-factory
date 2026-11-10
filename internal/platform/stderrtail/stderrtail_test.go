package stderrtail

import (
	"fmt"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

func write(t *Tail, parts ...string) {
	for _, p := range parts {
		_, _ = t.Write([]byte(p))
	}
}

func TestTail(t *testing.T) {
	specs.Describe(t, "stderrtail.Tail", func(s *specs.Spec) {
		s.It("has no lines when nothing was written", func(ctx *specs.Context) {
			ctx.Expect((&Tail{}).Lines()).To(specs.BeNil())
		})

		s.It("keeps lines written in pieces, the one not yet ended last, without CRs", func(ctx *specs.Context) {
			tail := &Tail{}
			write(tail, "first li", "ne\r\nsecond\n", "third")
			ctx.Expect(tail.Lines()).ToEqual([]string{"first line", "second", "third"})
		})

		s.It("keeps only the last MaxLines lines", func(ctx *specs.Context) {
			tail := &Tail{}
			want := []string{}
			for i := range 3 * MaxLines {
				write(tail, fmt.Sprintf("line %d\n", i))
				if i >= 2*MaxLines {
					want = append(want, fmt.Sprintf("line %d", i))
				}
			}
			ctx.Expect(tail.Lines()).ToEqual(want)
		})

		s.It("counts the line not yet ended among the last MaxLines", func(ctx *specs.Context) {
			tail := &Tail{}
			for i := range MaxLines {
				write(tail, fmt.Sprintf("line %d\n", i))
			}
			write(tail, "partial")
			got := tail.Lines()
			ctx.Expect(got).To(specs.HaveLen(MaxLines))
			ctx.Expect(got[0]).ToEqual("line 1")
			ctx.Expect(got[MaxLines-1]).ToEqual("partial")
		})

		s.It("cuts a line too long at MaxLineBytes and marks it", func(ctx *specs.Context) {
			tail := &Tail{}
			write(tail, strings.Repeat("a", 3*MaxLineBytes), strings.Repeat("b", MaxLineBytes), "\nnext\n")
			ctx.Expect(tail.Lines()).ToEqual([]string{strings.Repeat("a", MaxLineBytes) + Cut, "next"})
		})

		s.It("cuts a line on a character boundary", func(ctx *specs.Context) {
			tail := &Tail{}
			write(tail, strings.Repeat("a", MaxLineBytes-1)+"ñ and more\n")
			ctx.Expect(tail.Lines()).ToEqual([]string{strings.Repeat("a", MaxLineBytes-1) + Cut})
		})

		s.It("cuts the line not yet ended too", func(ctx *specs.Context) {
			tail := &Tail{}
			write(tail, strings.Repeat("x", 2*MaxLineBytes))
			ctx.Expect(tail.Lines()).ToEqual([]string{strings.Repeat("x", MaxLineBytes) + Cut})
		})
	})
}
