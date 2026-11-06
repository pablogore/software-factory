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
		job := domain.Job{ID: "job-1", Pipeline: "default", Stage: "build", Round: 1}
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
			_, err := st.db.ExecContext(bg, `CREATE TRIGGER refuse BEFORE INSERT ON events WHEN NEW.name = 'GatePassed'
				BEGIN SELECT RAISE(ABORT, 'refused'); END`)
			ctx.Expect(err).To(specs.BeNil())

			err = st.Apply(bg, job, []domain.Event{
				domain.StageEntered{Job: job.ID, Stage: domain.StageGate, At: at},
				domain.GatePassed{Job: job.ID, At: at},
			})

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			got, err := st.Load(bg, job.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(job)
			hist, err := st.History(bg, job.ID)
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
