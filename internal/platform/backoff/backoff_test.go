package backoff

import (
	"fmt"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"
)

type waitRow struct {
	failures int
	want     time.Duration
}

// The stores' contract runs the bus's and the outbox's retries in
// internal/adapters/out/sqlite; the cap is only reached past the retries they
// make, so it is checked here.
func TestBackoffWait(t *testing.T) {
	specs.Describe(t, "backoff wait", func(s *specs.Spec) {
		rows := []waitRow{
			{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {4, 8 * time.Second}, {5, 16 * time.Second},
			{6, 32 * time.Second}, {7, time.Minute}, {100, time.Minute},
		}
		specs.Table(s, rows, func(r waitRow) string {
			return fmt.Sprintf("after %d failures waits %s", r.failures, r.want)
		}, func(ctx *specs.Context, r waitRow) {
			ctx.Expect(Wait(r.failures)).ToEqual(r.want)
		})
	})
}
