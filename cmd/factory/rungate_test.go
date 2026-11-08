package main

import (
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// gateTokens issues, on f's j-1, a token for a builder that lists run_gate,
// as roles/builder.yaml does, and one for a reviewer without a tools list.
func gateTokens(ctx *specs.Context, f factoryd) (builder, reviewer string) {
	var err error
	builder, err = f.issuer.Issue(jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress", "run_gate"}, Round: 1})
	ctx.Expect(err).To(specs.BeNil())
	reviewer, err = f.issuer.Issue(jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Round: 1})
	ctx.Expect(err).To(specs.BeNil())
	return builder, reviewer
}

const gateReport = "checks fail for job j-1: `make check` exited 2 in 1.5s\n--- output ---\n--- FAIL: TestSave\n--- end of output ---\nrule broken: Makefile is protected\n"

func TestWorkerRunGate(t *testing.T) {
	specs.Describe(t, "factory worker run-gate", func(s *specs.Spec) {
		s.It("prints the run of the job's checks for a builder, the same the MCP tool run_gate answers", func(ctx *specs.Context) {
			f := start(ctx)
			f.gate.run = domain.ChecksRun{Command: "make check", ExitCode: 2, Duration: 1500 * time.Millisecond, Output: "--- FAIL: TestSave", Violations: []string{"Makefile is protected"}}
			builder, _ := gateTokens(ctx, f)

			r := cli(f.env("j-1", builder), "", "run-gate")
			text, isErr, err := f.call("run_gate", "j-1", builder, `{}`)

			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stdout).ToEqual(gateReport)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual(r.stdout)
			ctx.Expect(f.gate.checked()).ToEqual([]domain.JobID{"j-1", "j-1"})
			ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
		})

		s.It("exits 0 when the checks pass", func(ctx *specs.Context) {
			f := start(ctx)
			f.gate.run = domain.ChecksRun{Command: "make check", Output: "ok\n"}
			builder, _ := gateTokens(ctx, f)

			r := cli(f.env("j-1", builder), "", "run-gate")

			ctx.Expect(r.code).ToEqual(0)
			ctx.Expect(r.stdout).To(specs.StartWith("checks pass for job j-1: `make check` exited 0"))
		})

		s.It("refuses a reviewer's token, by the CLI and by MCP, running no check", func(ctx *specs.Context) {
			f := start(ctx)
			_, reviewer := gateTokens(ctx, f)

			r := cli(f.env("j-1", reviewer), "", "run-gate")
			text, isErr, err := f.call("run_gate", "j-1", reviewer, `{}`)

			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stdout).To(specs.BeEmpty())
			refusal := `refused (403): worker: run gate: job token not allowed: role "reviewer-code" may not use tool "run_gate"`
			ctx.Expect(strings.TrimSpace(r.stderr)).ToEqual("factory worker run-gate: " + refusal)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual(refusal)
			ctx.Expect(f.gate.checked()).To(specs.BeEmpty())
		})

		s.It("refuses a builder without run_gate in its tools, running no check", func(ctx *specs.Context) {
			f := start(ctx)
			r := cli(f.env("j-1", f.tokens["j-1"]), "", "run-gate")
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).To(specs.Contain(`role "builder" may not use tool "run_gate"`))
			ctx.Expect(f.gate.checked()).To(specs.BeEmpty())
		})

		s.It("takes no arguments, so the agent can pass nothing to the checks", func(ctx *specs.Context) {
			f := start(ctx)
			builder, _ := gateTokens(ctx, f)
			r := cli(f.env("j-1", builder), "", "run-gate", "make", "lint")
			ctx.Expect(r.code).ToEqual(2)
			ctx.Expect(r.stderr).To(specs.Contain("usage:"))
			ctx.Expect(f.gate.checked()).To(specs.BeEmpty())
		})

		s.It("does not call factoryd without a token", func(ctx *specs.Context) {
			f := start(ctx)
			r := cli(f.env("j-1", ""), "", "run-gate")
			ctx.Expect(r.code).ToEqual(2)
			ctx.Expect(r.stderr).To(specs.Contain("factory worker run-gate: no job token"))
			ctx.Expect(f.gate.checked()).To(specs.BeEmpty())
		})
	})
}
