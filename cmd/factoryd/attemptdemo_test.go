package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

func TestAttemptDemo(t *testing.T) {
	specs.Describe(t, "factoryd attempt-demo", func(s *specs.Spec) {
		s.It("shows a retry with the error and the result recorded, then a retry and a failed attempt", func(ctx *specs.Context) {
			var out bytes.Buffer
			ctx.Expect(attemptDemo(context.Background(), &out)).To(specs.BeNil())
			demo1, demo2, _ := strings.Cut(out.String(), "== job demo-2")
			ctx.Expect(strings.Count(demo1, "-> prompt")).ToEqual(2)
			ctx.Expect(demo1).To(specs.Contain(`last turn: your result was refused: worker: build result: invalid value: status "DONE"`))
			ctx.Expect(demo1).To(specs.Contain("result recorded: true; job demo-1: stage gate"))
			ctx.Expect(strings.Count(demo2, "-> prompt")).ToEqual(2)
			ctx.Expect(demo2).To(specs.Contain("last turn: no result submitted."))
			ctx.Expect(demo2).To(specs.Contain(`result recorded: false; job demo-2: stage build, events [AttemptFailed(role builder, reason "no result submitted")]`))
		})
	})
}
