package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// These cases need the database itself: a write SQLite refuses mid
// transaction, and a row no store wrote.
func TestStoreDatabase(t *testing.T) {
	specs.Describe(t, "sqlite.Store database", func(s *specs.Spec) {
		bg := context.Background()
		job := domain.Job{ID: "job-1", Pipeline: "default", Stage: domain.StageGate, Round: 1}
		p := domain.Pipeline{Name: "default", Stages: []domain.Stage{
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
		}}
		at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
		open := func(ctx *specs.Context) *Store {
			st, err := Open(bg, filepath.Join(ctx.Testing().TempDir(), "factory.db"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = st.Close() })
			ctx.Expect(st.Create(bg, job)).To(specs.BeNil())
			return st
		}

		s.It("rolls back the state and earlier events when SQLite refuses an event insert", func(ctx *specs.Context) {
			st := open(ctx)
			// A failed gate in round 1 is RoundStarted then StageEntered: the
			// second insert fails after the state and the first were written.
			_, err := st.db.ExecContext(bg, `CREATE TRIGGER refuse BEFORE INSERT ON events WHEN NEW.name = 'StageEntered'
				BEGIN SELECT RAISE(ABORT, 'refused'); END`)
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, job.ID, 1, p, domain.GateFailed{Job: job.ID, At: at})

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			got, v, err := st.Load(bg, job.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(job)
			ctx.Expect(v).ToEqual(ports.Version(1))
			hist, err := st.History(bg, job.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})

		s.It("saves neither the override record nor the state when SQLite refuses its merge request", func(ctx *specs.Context) {
			st, err := Open(bg, filepath.Join(ctx.Testing().TempDir(), "factory.db"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = st.Close() })
			waiting := domain.Job{ID: "job-1", Pipeline: "default", Stage: domain.StageNeedsHuman, Round: 3, WaitSeq: 1, Cause: domain.CauseChanges}
			ctx.Expect(st.Create(bg, waiting)).To(specs.BeNil())
			// MergeOverridden is inserted first, so the refusal comes after it
			// and the state were written.
			_, err = st.db.ExecContext(bg, `CREATE TRIGGER refuse BEFORE INSERT ON events WHEN NEW.name = 'MergeRequested'
				BEGIN SELECT RAISE(ABORT, 'refused'); END`)
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, waiting.ID, 1, p, domain.OverrideRequested{Job: waiting.ID, By: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Reason: "ship it", WaitSeq: 1, At: at})

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			got, v, err := st.Load(bg, waiting.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(waiting)
			ctx.Expect(v).ToEqual(ports.Version(1))
			hist, err := st.History(bg, waiting.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})

		s.It("reading a row of an unknown event type is an error, never an empty event", func(ctx *specs.Context) {
			st := open(ctx)
			_, err := st.db.ExecContext(bg, `INSERT INTO events (job_id, name, at, data) VALUES (?, 'Renamed', ?, '{}')`,
				string(job.ID), at.Format(time.RFC3339Nano))
			ctx.Expect(err).To(specs.BeNil())

			hist, err := st.History(bg, job.ID)

			ctx.Expect(err).To(specs.MatchError(ports.ErrUnknownEvent))
			ctx.Expect(hist).To(specs.BeNil())
		})

		s.It("reading a ResultSubmitted of an unknown result kind is an error", func(ctx *specs.Context) {
			st := open(ctx)
			_, err := st.db.ExecContext(bg, `INSERT INTO events (job_id, name, at, data) VALUES (?, 'ResultSubmitted', ?, ?)`,
				string(job.ID), at.Format(time.RFC3339Nano), `{"job":"job-1","role":"x","kind":"poem","result":{},"at":"2026-11-06T20:00:00Z"}`)
			ctx.Expect(err).To(specs.BeNil())

			_, err = st.History(bg, job.ID)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}
