package acp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	// The rows name time zones, which a machine without them still loads.
	_ "time/tzdata"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// limitRow is an agent's error answer to a prompt and when the adapter says
// it may go on; a zero resume is an error that is no limit.
type limitRow struct {
	name   string
	err    acpfake.Error
	resume time.Time
}

// ended is the last event of a turn the fake agent answers with err.
func ended(ctx *specs.Context, err acpfake.Error) domain.AgentProgressed {
	a, _ := fakeAgent(ctx, acpfake.Script{Turns: []acpfake.Turn{{Error: &err}}})
	sess, startErr := a.Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir()})
	ctx.Expect(startErr).To(specs.BeNil())
	ctx.Cleanup(func() { _ = sess.Close() })
	ch, promptErr := sess.Prompt(context.Background(), "build")
	ctx.Expect(promptErr).To(specs.BeNil())
	got := drain(ctx, ch)
	ctx.Expect(got).To(specs.HaveLen(1))
	return got[0]
}

func TestAgentLimit(t *testing.T) {
	// epoch is Saturday 2026-11-07 13:00 UTC.
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	reset := epoch.Add(5 * time.Hour)
	wait := epoch.Add(acp.DefaultLimitWait)
	rows := []limitRow{
		{"claude: usage limit with the unix time it resets", acpfake.Error{Code: -32603, Message: fmt.Sprintf("Claude AI usage limit reached|%d", reset.Unix())}, reset},
		{"claude: 5-hour limit that resets at an hour of the day", acpfake.Error{Code: -32603, Message: "5-hour limit reached ∙ resets 3pm"}, time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC)},
		{"claude: limit that resets at a time in its own time zone", acpfake.Error{Code: -32603, Message: "Internal error", Data: json.RawMessage(`"You've hit your limit · resets 10:30am (America/New_York)"`)}, time.Date(2026, 11, 7, 10, 30, 0, 0, newYork).UTC()},
		{"claude: weekly limit that resets on a day of the week", acpfake.Error{Code: -32603, Message: "Weekly limit reached ∙ resets Mon 9am"}, time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)},
		{"claude: the API's rate_limit_error, which says not when", acpfake.Error{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"details":"API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}"}`)}, wait},
		{"codex: usage limit that says to try again in words", acpfake.Error{Code: -32603, Message: "You've hit your usage limit. Upgrade to Pro (https://openai.com/chatgpt/pricing) or try again in 1 day 2 hours 5 minutes."}, epoch.Add(26*time.Hour + 5*time.Minute)},
		{"codex: rate_limit_exceeded with a Go duration", acpfake.Error{Code: -32603, Message: "stream error: rate_limit_exceeded: Please try again in 6.5s."}, epoch.Add(6500 * time.Millisecond)},
		{"codex: 429 Too Many Requests, which says not when", acpfake.Error{Code: -32603, Message: "exceeded retry limit, last status: 429 Too Many Requests"}, wait},
		{"gemini: code 429 with Google's retryDelay", acpfake.Error{Code: 429, Message: "Quota exceeded for quota metric 'Gemini 2.5 Pro Requests'", Data: json.RawMessage(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"retryDelay":"23s"}]}}`)}, epoch.Add(23 * time.Second)},
		{"gemini: RESOURCE_EXHAUSTED that says to retry in a while", acpfake.Error{Code: -32603, Message: "RESOURCE_EXHAUSTED: Please retry in 41.2s."}, epoch.Add(41200 * time.Millisecond)},
		{"any agent: rate limited", acpfake.Error{Code: -32603, Message: "rate limited"}, wait},
		{"no limit: a tool that failed", acpfake.Error{Code: -32603, Message: "Internal error", Data: json.RawMessage(`"tool failed: exit status 1"`)}, time.Time{}},
		{"no limit: an agent that needs a login", acpfake.Error{Code: -32000, Message: "Authentication required"}, time.Time{}},
	}
	specs.Describe(t, "acp.Agent with an agent that answers a prompt with an error", func(s *specs.Spec) {
		specs.Table(s, rows, func(r limitRow) string { return r.name }, func(ctx *specs.Context, r limitRow) {
			got := ended(ctx, r.err)

			ctx.Expect(got.Kind).ToEqual(domain.ProgressEnded)
			if r.resume.IsZero() {
				ctx.Expect(got.StopReason).ToEqual("")
				ctx.Expect(got.Error).To(specs.Contain(r.err.Message))
				ctx.Expect(got.ResumeAt.IsZero()).To(specs.BeTrue())
				return
			}
			ctx.Expect(got.StopReason).ToEqual(domain.StopLimited)
			ctx.Expect(got.Error).ToEqual("")
			ctx.Expect(got.Reason).To(specs.Contain(r.err.Message))
			ctx.Expect(got.ResumeAt.Equal(r.resume)).To(specs.BeTrue())
		})

		s.It("cuts an agent's long answer to a short reason", func(ctx *specs.Context) {
			long := "rate limited: " + strings.Repeat("x", 2000)

			got := ended(ctx, acpfake.Error{Code: -32603, Message: long})

			ctx.Expect(got.StopReason).ToEqual(domain.StopLimited)
			ctx.Expect(len(got.Reason) < 600).To(specs.BeTrue())
		})
	})
}
