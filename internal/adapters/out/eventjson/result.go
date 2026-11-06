package eventjson

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// resultSubmitted is ResultSubmitted on disk: Kind names the Result's
// concrete type.
type resultSubmitted struct {
	Job    domain.JobID    `json:"job"`
	Role   string          `json:"role"`
	Kind   domain.Kind     `json:"kind"`
	Result json.RawMessage `json:"result"`
	At     time.Time       `json:"at"`
}

var results = map[domain.Kind]reflect.Type{
	domain.KindIntake:  reflect.TypeFor[domain.IntakeResult](),
	domain.KindSpec:    reflect.TypeFor[domain.SpecResult](),
	domain.KindBuild:   reflect.TypeFor[domain.BuildResult](),
	domain.KindReview:  reflect.TypeFor[domain.ReviewResult](),
	domain.KindApprove: reflect.TypeFor[domain.ApproveResult](),
	domain.KindTask:    reflect.TypeFor[domain.TaskResult](),
}

func encodeResultSubmitted(e domain.ResultSubmitted) ([]byte, error) {
	if e.Result == nil {
		return nil, fmt.Errorf("%w: result", domain.ErrMissingField)
	}
	if t, ok := results[e.Result.Kind()]; !ok || reflect.TypeOf(e.Result) != t {
		return nil, fmt.Errorf("%w: result %T of kind %q", domain.ErrInvalidValue, e.Result, e.Result.Kind())
	}
	r, err := json.Marshal(e.Result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resultSubmitted{Job: e.Job, Role: e.Role, Kind: e.Result.Kind(), Result: r, At: e.At})
}

func decodeResultSubmitted(data []byte) (domain.Event, error) {
	var w resultSubmitted
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	t, ok := results[w.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: result kind %q", domain.ErrInvalidValue, w.Kind)
	}
	r := reflect.New(t)
	if err := json.Unmarshal(w.Result, r.Interface()); err != nil {
		return nil, fmt.Errorf("result: %w", err)
	}
	return domain.ResultSubmitted{Job: w.Job, Role: w.Role, Result: r.Elem().Interface().(domain.Result), At: w.At}, nil
}
