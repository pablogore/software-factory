package decide_test

import (
	"reflect"
	"testing"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

var fuzzStages = []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageMerge}

// FuzzDecide checks that the same input always gives the same output and that
// Decide never changes the job it receives.
func FuzzDecide(f *testing.F) {
	for i := range happyPath() {
		f.Add(uint8(i), uint8(i), uint8(0))
	}
	f.Fuzz(func(t *testing.T, stage, event, done uint8) {
		steps := happyPath()
		job := newJob()
		job.Stage = fuzzStages[int(stage)%len(fuzzStages)]
		job.Passed = append([]string(nil), reviewers[:int(done)%len(reviewers)]...)
		before := newJob()
		before.Stage, before.Passed = job.Stage, append([]string(nil), job.Passed...)
		ev := steps[int(event)%len(steps)].ev

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
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
