package decide_test

import (
	"reflect"
	"testing"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

var fuzzStages = []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageMerge, domain.StageNeedsHuman}

// fuzzEvents are the happy path's events plus a CHANGES from a reviewer that
// has not answered in any fuzzed job, a failing gate, a person's approval and
// messages from every kind of author.
func fuzzEvents() []domain.Event {
	var evs []domain.Event
	for _, st := range happyPath() {
		evs = append(evs, st.ev)
	}
	return append(evs, changes("security-deep", 1), domain.GateFailed{Job: jobID, At: at(1)}, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(1)},
		message(domain.AuthorPerson, true, 1), message(domain.AuthorPerson, false, 1),
		message(domain.AuthorBot, true, 1), message(domain.AuthorCI, true, 1))
}

// FuzzDecide checks that the same input always gives the same output, that
// Decide never changes the job it receives and that no round goes past the
// review stage's max_rounds, and that a message not from an allowed person
// changes nothing and is not an error.
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
		before := newJob()
		before.Stage, before.Round, before.Passed = job.Stage, job.Round, append([]string(nil), job.Passed...)
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
		if after := decide.Apply(job, first); after.Round > 3 {
			t.Fatalf("round %d past max_rounds 3", after.Round)
		}
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
