package decide_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

var fuzzStages = []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageMerge, domain.StageDone, domain.StageNeedsHuman, domain.StageNeedsInfo, domain.StageStopped, domain.StageRejected}

// fuzzEvents are the happy path's events plus a CHANGES from a reviewer that
// has not answered in any fuzzed job, a failing gate and specgate, a spec
// asking for a length exception, a specgate rejection count, a person's approval, an intake NEEDS_INFO and REJECT, a builder BLOCKED, messages (and
// so answers) and direction marks from every kind of author, with and without a name, stop requests with and without a
// name, overrides from every kind of author and without a name or a reason,
// a completed merge and every missing or nil event of nilEvents.
func fuzzEvents() []domain.Event {
	var evs []domain.Event
	for _, st := range happyPath() {
		evs = append(evs, st.ev)
	}
	evs = append(evs, changes("security-deep", 1), domain.GateFailed{Job: jobID, At: at(1)}, domain.SpecgateFailed{Job: jobID, At: at(1)}, exceptionAsked(1),
		domain.SpecRejected{Job: jobID, Rejections: 1, At: at(1)}, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(1)},
		message(domain.AuthorPerson, true, 1), message(domain.AuthorPerson, false, 1),
		message(domain.AuthorBot, true, 1), message(domain.AuthorCI, true, 1),
		direction(domain.AuthorPerson, true, 1), direction(domain.AuthorPerson, false, 1), direction(domain.AuthorBot, true, 1),
		signed(message(domain.AuthorPerson, true, 1), ""), signed(message(domain.AuthorPerson, false, 1), " "),
		signed(direction(domain.AuthorPerson, true, 1), " \t"), signed(direction(domain.AuthorBot, true, 1), ""),
		stop("pablogore", 1), stop("", 1), merged(1), needsInfo(), blockedBuild("cannot reach the API", 1), rejectIntake("duplicate of #12", 1),
		override(domain.AuthorPerson, true, 1), override(domain.AuthorPerson, false, 1), override(domain.AuthorBot, true, 1), override(domain.AuthorCI, true, 1),
		domain.OverrideRequested{Job: jobID, By: " ", AuthorKind: domain.AuthorPerson, Allowed: true, Reason: overrideReason, At: at(1)},
		domain.OverrideRequested{Job: jobID, By: "pablogore", AuthorKind: domain.AuthorPerson, Allowed: true, Reason: "", At: at(1)})
	for _, r := range nilEvents() {
		evs = append(evs, r.ev)
	}
	return evs
}

// FuzzDecide checks that the same input always gives the same output, that
// Decide never changes the job it receives and that no round goes past the
// review stage's max_rounds, that a message not from an allowed person
// changes nothing and is not an error, that an allowed person's message
// without a name is refused unless the job is stopped or rejected, that a job a decision moves records an
// asker exactly when it lands in needs-info, that only a job in needs-human
// waits on a length exception, that every needs-human it emits carries one
// of HumanCauses with each mark set exactly when its cause is, that a job out
// of needs-human keeps no cause, that a person's OK requests the merge only
// after an approver ESCALATE, that an override not from an allowed person
// changes nothing and is not an error, that one requests the merge only after
// CHANGES and always with its history entry first, and that a missing or nil
// event is an error, never a panic.
func FuzzDecide(f *testing.F) {
	for i := range fuzzEvents() {
		f.Add(uint8(i), uint8(i), uint8(0), uint8(1))
	}
	f.Add(uint8(5), uint8(len(fuzzEvents())-1), uint8(4), uint8(3))
	f.Fuzz(func(t *testing.T, stage, event, done, round uint8) {
		job := newJob()
		job.Stage = fuzzStages[int(stage)%len(fuzzStages)]
		job.Round = 1 + int(round)%3
		job.Passed = append([]string(nil), reviewers[:int(done)%len(reviewers)]...)
		job.InfoFrom = fuzzAsker(job.Stage, done)
		job.SpecException = job.Stage == domain.StageNeedsHuman && done%2 == 1
		job.Cause = fuzzCause(job.Stage, round)
		before := newJob()
		before.Stage, before.Round, before.Passed = job.Stage, job.Round, append([]string(nil), job.Passed...)
		before.InfoFrom, before.SpecException, before.Cause = job.InfoFrom, job.SpecException, job.Cause
		evs := fuzzEvents()
		ev := evs[int(event)%len(evs)]

		first, err1 := decide.Decide(job, ev, defaultPipeline())
		second, err2 := decide.Decide(job, ev, defaultPipeline())

		if !reflect.DeepEqual(first, second) || errText(err1) != errText(err2) {
			t.Fatalf("same input, different output: %v, %v / %v, %v", first, err1, second, err2)
		}
		if !reflect.DeepEqual(job, before) {
			t.Fatalf("job changed: %+v, was %+v", job, before)
		}
		if err1 != nil && len(first) > 0 {
			t.Fatalf("events returned with an error: %v", first)
		}
		if m, ok := ev.(domain.MessageReceived); ok && (!m.Allowed || m.AuthorKind != domain.AuthorPerson) && (err1 != nil || len(first) > 0) {
			t.Fatalf("message from %s (allowed %v) gave %v, %v", m.AuthorKind, m.Allowed, first, err1)
		}
		if m, ok := ev.(domain.MessageReceived); ok && m.Allowed && m.AuthorKind == domain.AuthorPerson && strings.TrimSpace(m.Author) == "" &&
			job.Stage != domain.StageStopped && job.Stage != domain.StageRejected && !errors.Is(err1, domain.ErrMissingField) {
			t.Fatalf("message without a name in stage %q gave %v, %v", job.Stage, first, err1)
		}
		if missing(ev) && err1 == nil {
			t.Fatalf("missing event %#v accepted in stage %q", ev, job.Stage)
		}
		after := decide.Apply(job, first)
		if after.Round > 3 {
			t.Fatalf("round %d past max_rounds 3", after.Round)
		}
		if len(first) > 0 && (after.Stage == domain.StageNeedsInfo) != (after.InfoFrom != "") {
			t.Fatalf("stage %q with asker %q after %s", after.Stage, after.InfoFrom, ev.EventName())
		}
		if after.SpecException && after.Stage != domain.StageNeedsHuman {
			t.Fatalf("stage %q waiting on a length exception after %s", after.Stage, ev.EventName())
		}
		for _, out := range first {
			if nh, ok := out.(domain.NeedsHuman); ok && (nh.Cause.Validate() != nil ||
				nh.SpecException != (nh.Cause == domain.CauseSpecException) || nh.SpecEscalated != (nh.Cause == domain.CauseSpecEscalated)) {
				t.Fatalf("needs-human with cause %q, exception %v, escalated %v after %s", nh.Cause, nh.SpecException, nh.SpecEscalated, ev.EventName())
			}
		}
		if _, ok := ev.(domain.HumanApproved); ok && job.Cause != domain.CauseEscalated && slices.ContainsFunc(first, func(out domain.Event) bool {
			_, merge := out.(domain.MergeRequested)
			return merge
		}) {
			t.Fatalf("OK with cause %q requested the merge", job.Cause)
		}
		if o, ok := ev.(domain.OverrideRequested); ok && (!o.Allowed || o.AuthorKind != domain.AuthorPerson) && (err1 != nil || len(first) > 0) {
			t.Fatalf("override from %s (allowed %v) gave %v, %v", o.AuthorKind, o.Allowed, first, err1)
		}
		if o, ok := ev.(domain.OverrideRequested); ok && slices.ContainsFunc(first, func(out domain.Event) bool {
			_, merge := out.(domain.MergeRequested)
			return merge
		}) && (job.Cause != domain.CauseChanges || first[0] != domain.MergeOverridden{Job: jobID, By: o.By, Reason: o.Reason, At: o.At}) {
			t.Fatalf("override with cause %q gave %v", job.Cause, first)
		}
		if after.Cause != "" && after.Stage != domain.StageNeedsHuman {
			t.Fatalf("stage %q keeps cause %q after %s", after.Stage, after.Cause, ev.EventName())
		}
	})
}

// fuzzCause gives a job in needs-human one of HumanCauses, none or an
// unknown one; a job anywhere else has none.
func fuzzCause(stage string, round uint8) domain.HumanCause {
	if stage != domain.StageNeedsHuman {
		return ""
	}
	causes := append(domain.HumanCauses(), "", "approved")
	return causes[int(round)%len(causes)]
}

// FuzzStopped checks that a stopped job never moves: whatever the event,
// including another stop, an answer or a message with any author, text or
// mark, Decide returns no events and the job stays as it was, even with an
// asker left recorded on it.
func FuzzStopped(f *testing.F) {
	for i := range fuzzEvents() {
		f.Add(uint8(i), uint8(0), uint8(1), "pablogore", "rename the flag", true)
	}
	f.Add(uint8(0), uint8(3), uint8(2), "", "", false)
	f.Fuzz(func(t *testing.T, event, done, round uint8, by, text string, allowed bool) {
		job := newJob()
		job.Stage, job.Round = domain.StageStopped, 1+int(round)%3
		job.Passed = append([]string(nil), reviewers[:int(done)%len(reviewers)]...)
		job.InfoFrom = fuzzAsker(domain.StageNeedsInfo, done)
		before := newJob()
		before.Stage, before.Round, before.Passed = job.Stage, job.Round, append([]string(nil), job.Passed...)
		before.InfoFrom = job.InfoFrom
		kinds := []domain.AuthorKind{domain.AuthorPerson, domain.AuthorBot, domain.AuthorCI, ""}
		m := domain.MessageReceived{Job: jobID, Provider: "github", Thread: "pr/88", Author: by,
			AuthorKind: kinds[int(event)%len(kinds)], Allowed: allowed, Text: text, At: at(1)}
		evs := append(fuzzEvents(), stop(by, 1), domain.HumanApproved{Job: jobID, By: by, At: at(1)}, m)
		ev := evs[int(event)%len(evs)]

		out, _ := decide.Decide(job, ev, defaultPipeline())

		if len(out) > 0 {
			t.Fatalf("stopped job moved on %s: %v", ev.EventName(), out)
		}
		if !reflect.DeepEqual(job, before) || !reflect.DeepEqual(decide.Apply(job, out), before) {
			t.Fatalf("stopped job changed on %s: %+v, was %+v", ev.EventName(), job, before)
		}
	})
}

// FuzzDropped checks that a slice a direction dropped never moves again:
// whatever the event, including another direction, an approval, a merge or a
// message with any author, text or mark, Decide returns no events and the job
// stays rejected.
func FuzzDropped(f *testing.F) {
	for i := range fuzzEvents() {
		f.Add(uint8(i), uint8(1), "pablogore", "[direction] build S2", true, true)
	}
	f.Add(uint8(0), uint8(2), "", "", false, false)
	f.Fuzz(func(t *testing.T, event, round uint8, by, text string, allowed, marked bool) {
		job := sliceJob()
		job.Stage, job.Round = domain.StageRejected, 1+int(round)%3
		before := job
		kinds := []domain.AuthorKind{domain.AuthorPerson, domain.AuthorBot, domain.AuthorCI, ""}
		m := domain.MessageReceived{Job: jobID, Provider: "github", Thread: "pr/88", Author: by,
			AuthorKind: kinds[int(event)%len(kinds)], Allowed: allowed, Direction: marked, Text: text, At: at(1)}
		evs := append(fuzzEvents(), stop(by, 1), domain.HumanApproved{Job: jobID, By: by, At: at(1)}, m)
		ev := evs[int(event)%len(evs)]

		out, _ := decide.Decide(job, ev, defaultPipeline())

		if len(out) > 0 {
			t.Fatalf("dropped slice moved on %s: %v", ev.EventName(), out)
		}
		if !reflect.DeepEqual(job, before) || !reflect.DeepEqual(decide.Apply(job, out), before) {
			t.Fatalf("dropped slice changed on %s: %+v, was %+v", ev.EventName(), job, before)
		}
	})
}

// fuzzAsker is the stage recorded as asking for a job in stage: one of the
// pipeline's stages or an empty or bogus one in needs-info, none elsewhere.
func fuzzAsker(stage string, n uint8) string {
	if stage != domain.StageNeedsInfo {
		return ""
	}
	askers := []string{"intake", "spec", "", domain.StageDone}
	return askers[int(n)%len(askers)]
}

// missing reports whether ev is one of nilEvents: nil, a nil pointer or a
// result event carrying a nil result.
func missing(ev domain.Event) bool {
	if r, ok := ev.(domain.ResultSubmitted); ok {
		return nilPointer(r.Result)
	}
	return ev == nil || nilPointer(ev)
}

func nilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
