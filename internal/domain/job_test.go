package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestJob(t *testing.T) {
	specs.Describe(t, "Job", func(s *specs.Spec) {
		s.It("reads back from JSON equal to the original", func(ctx *specs.Context) {
			job := domain.Job{
				ID:         "001-e01-1-modelo-de",
				Title:      "Domain model",
				Pipeline:   "default",
				Stage:      "review",
				Round:      2,
				Initiative: "017-e01-6-decide-maneja",
				Origin:     domain.Origin{Provider: "github", Thread: "issue/23", Author: "pablogore"},
				Subscriptions: []domain.Subscription{
					{Provider: "github", Thread: "issue/23", Kind: domain.ThreadIssue},
					{Provider: "github", Thread: "pr/24", Kind: domain.ThreadPR},
					{Provider: "discord", Thread: "1234", Kind: domain.ThreadChat},
				},
			}

			data, err := json.Marshal(job)
			ctx.Expect(err).To(specs.BeNil())
			var back domain.Job
			ctx.Expect(json.Unmarshal(data, &back)).To(specs.BeNil())

			ctx.Expect(back).ToEqual(job)
		})
	})
}

func TestKind(t *testing.T) {
	specs.Describe(t, "Kind", func(s *specs.Spec) {
		s.It("accepts the six kinds", func(ctx *specs.Context) {
			for _, k := range domain.Kinds {
				ctx.Expect(k.Validate()).To(specs.BeNil())
			}
			ctx.Expect(domain.Kinds).To(specs.HaveLen(6))
		})

		s.It("rejects an unknown kind", func(ctx *specs.Context) {
			ctx.Expect(domain.Kind("deploy").Validate()).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}

type invalidJobRow struct {
	name     string
	edit     func(*domain.Job)
	sentinel error
	field    string
}

func TestJobValidate(t *testing.T) {
	specs.Describe(t, "Job.Validate", func(s *specs.Spec) {
		valid := func() domain.Job {
			return domain.Job{ID: "job-1", Pipeline: "default", Stage: domain.StageNeedsHuman, Round: 1, WaitSeq: 1, Cause: domain.CauseBlocked}
		}

		s.It("accepts a job that keeps every invariant", func(ctx *specs.Context) {
			ctx.Expect(valid().Validate()).To(specs.BeNil())
			ctx.Expect(domain.Job{ID: "job-1", Stage: "build", Round: 1}.Validate()).To(specs.BeNil())
		})

		rows := []invalidJobRow{
			{"refuses needs-human with wait_seq 0", func(j *domain.Job) { j.WaitSeq = 0 }, domain.ErrInvalidValue, "wait_seq"},
			{"refuses needs-info with wait_seq 0", func(j *domain.Job) { j.Stage, j.Cause, j.WaitSeq = domain.StageNeedsInfo, "", 0 }, domain.ErrInvalidValue, "wait_seq"},
			{"refuses done with wait_seq 0", func(j *domain.Job) { j.Stage, j.Cause, j.WaitSeq = domain.StageDone, "", 0 }, domain.ErrInvalidValue, "wait_seq"},
			{"refuses needs-human without a cause", func(j *domain.Job) { j.Cause = "" }, domain.ErrMissingField, "cause"},
			{"refuses an unknown cause", func(j *domain.Job) { j.Cause = "bored" }, domain.ErrInvalidValue, "cause"},
			{"refuses an unknown cause outside needs-human", func(j *domain.Job) { j.Stage, j.Cause = "build", "bored" }, domain.ErrInvalidValue, "cause"},
		}
		specs.Table(s, rows, func(r invalidJobRow) string { return r.name }, func(ctx *specs.Context, r invalidJobRow) {
			job := valid()
			r.edit(&job)

			err := job.Validate()

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			ctx.Expect(err.Error()).To(specs.Contain(r.field))
		})
	})
}
