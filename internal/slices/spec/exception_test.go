package spec_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/slices/spec"
)

// The specgate cases of factory/tests/test_spec_exception.py on main. Its
// end-to-end cases run here through Decide, which owns the wait.

const defaultReason = "El mapeo y el bloqueo no sirven por separado: partirlo deja un slice sin Demo."

// longSpec is main's long_spec; an empty reason means no exception section.
func longSpec(criteria, words int, reason string) string {
	var crit strings.Builder
	for i := 1; i <= criteria; i++ {
		fmt.Fprintf(&crit, "%d. %s\n", i, repeat("palabra", words))
	}
	exc := ""
	if reason != "" {
		exc = "\n## Excepción de largo\n" + reason + "\n"
	}
	return "STATUS: READY\n\n# S1: hacer algo\n\n## Valor\nQuien compra puede hacer algo.\n\n" +
		"## Criterios de aceptación\n" + crit.String() + "\n## Demo\n1. Abrir la pantalla y verlo.\n" + exc
}

func exceptionRows() []gateRow {
	loose := strings.Replace(longSpec(1, 25, defaultReason), "## Demo", "Nota suelta: "+strings.Repeat("x ", 20)+"\n\n## Demo", 1)
	return []gateRow{
		{"without a reason it fails", longSpec(5, 40, ""), false, false, "criterion 1 has 40 words"},
		{"past the exception's criteria it fails even with the OK", longSpec(6, 40, defaultReason), true, false, "(with the exception)"},
		{"past the exception's criterion words it fails", longSpec(5, 46, defaultReason), false, false, "SPECGATE: FAIL"},
		{"a short spec with a reason just passes", longSpec(2, 10, defaultReason), false, true, "SPECGATE: PASS"},
		{"the reason must be short", longSpec(5, 40, repeat("porque", 41)), false, false, "`## Excepción de largo` has 41 words"},
		{"a short spec with a long reason passes", longSpec(2, 20, repeat("razon", 50)), false, true, "SPECGATE: PASS"},
		{"the exception's text doesn't count toward the lines", longSpec(2, 20, strings.Repeat("motivo\n", 11)+"motivo"), false, true, "SPECGATE: PASS"},
		{"text after a blank line isn't part of the criterion", loose, false, true, "SPECGATE: PASS"},
	}
}

func atSpecgate() domain.Job {
	return domain.Job{ID: "j-1", Title: "Long spec", Pipeline: "default", Stage: domain.StageSpecgate, Round: 1}
}

func pipeline() domain.Pipeline {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "intake", Kind: domain.KindIntake, Roles: []string{"intake"}},
		{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
	}}
}

var at = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

func TestSpecgateException(t *testing.T) {
	specs.Describe(t, "specgate on a spec asking for a length exception", func(s *specs.Spec) {
		specs.Table(s, exceptionRows(), func(r gateRow) string { return r.name }, checkRow)

		s.It("asks a person, with the reason and the normal limits it is over", func(ctx *specs.Context) {
			rep := spec.Check(longSpec(5, 40, defaultReason), false)

			ctx.Expect(rep.Verdict).ToEqual(spec.VerdictException)
			ctx.Expect(rep.Reason).ToEqual(defaultReason)
			out := rep.String()
			ctx.Expect(out).To(specs.Contain("SPECGATE: EXCEPTION"))
			ctx.Expect(out).To(specs.Contain("> El mapeo y el bloqueo"))
			ctx.Expect(out).To(specs.Contain("- 5 numbered criteria; need 1 to 4"))
		})

		s.It("passes the same spec once a person accepted the exception", func(ctx *specs.Context) {
			ctx.Expect(spec.Check(longSpec(5, 40, defaultReason), true)).ToEqual(spec.Report{Verdict: spec.VerdictPass})
		})

		s.It("leaves the job waiting for a person, without counting a rejection", func(ctx *specs.Context) {
			job := atSpecgate()

			evs, err := decide.Decide(job, spec.Check(longSpec(5, 40, defaultReason), false).Event(job.ID, at), pipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: job.ID, Reason: "spec asks for a length exception", Cause: domain.CauseSpecException, At: at}})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(after.SpecRejections).ToEqual(0)
		})

		s.It("sends a failing spec back to be rewritten", func(ctx *specs.Context) {
			job := atSpecgate()

			evs, err := decide.Decide(job, spec.Check(longSpec(5, 40, ""), false).Event(job.ID, at), pipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(decide.Apply(job, evs).Stage).ToEqual("spec")
		})

		s.It("lets a passing spec go on to build", func(ctx *specs.Context) {
			job := atSpecgate()

			evs, err := decide.Decide(job, spec.Check(longSpec(5, 40, defaultReason), true).Event(job.ID, at), pipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(decide.Apply(job, evs).Stage).ToEqual("build")
		})
	})
}
