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
// transaction, after the state and the events or before the actions.
func TestOutboxDatabase(t *testing.T) {
	specs.Describe(t, "sqlite.Store outbox database", func(s *specs.Spec) {
		bg := context.Background()
		job := domain.Job{ID: "job-1", Pipeline: "default", Stage: domain.StageGate, Round: 1}
		p := domain.Pipeline{Name: "default", Stages: []domain.Stage{
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
		}}
		at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
		act := ports.Action{Key: "job-1/comment/1", Kind: "comment", Payload: "gate failed"}
		open := func(ctx *specs.Context, trigger string) *Store {
			st, err := Open(bg, filepath.Join(ctx.Testing().TempDir(), "factory.db"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = st.Close() })
			ctx.Expect(st.Create(bg, job)).To(specs.BeNil())
			_, err = st.db.ExecContext(bg, trigger)
			ctx.Expect(err).To(specs.BeNil())
			return st
		}
		// unsaved checks that neither the state, any event nor any action
		// of the failed Apply exists.
		unsaved := func(ctx *specs.Context, st *Store) {
			got, v, err := st.Load(bg, job.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(job)
			ctx.Expect(v).ToEqual(ports.Version(1))
			hist, err := st.History(bg, job.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
			qs, err := st.Actions(bg, job.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(qs).To(specs.BeEmpty())
		}

		s.It("rolls back the state and its events when SQLite refuses the action insert", func(ctx *specs.Context) {
			st := open(ctx, `CREATE TRIGGER refuse BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT, 'refused'); END`)

			_, _, err := st.Apply(bg, job.ID, 1, p, domain.GateFailed{Job: job.ID, Run: domain.ChecksRun{Command: "make check", ExitCode: 2}, At: at}, act)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			unsaved(ctx, st)
		})

		s.It("saves no action when SQLite refuses an event insert of the same state change", func(ctx *specs.Context) {
			st := open(ctx, `CREATE TRIGGER refuse BEFORE INSERT ON events WHEN NEW.name = 'StageEntered'
				BEGIN SELECT RAISE(ABORT, 'refused'); END`)

			_, _, err := st.Apply(bg, job.ID, 1, p, domain.GateFailed{Job: job.ID, Run: domain.ChecksRun{Command: "make check", ExitCode: 2}, At: at}, act)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			unsaved(ctx, st)
		})
	})
}
