// Package eventjson stores domain events as JSON in an envelope that names
// their type, so they decode back to the concrete type. The SQLite store and
// the in-memory fake share it, so both refuse the same unknown events.
package eventjson

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// Envelope is one encoded event: its EventName, its own time and its fields.
type Envelope struct {
	Name string
	At   time.Time
	Data []byte
}

type codec struct {
	typ    reflect.Type
	decode func([]byte) (domain.Event, error)
}

func register[E domain.Event]() (string, codec) {
	var zero E
	return zero.EventName(), codec{reflect.TypeFor[E](), func(data []byte) (domain.Event, error) {
		var e E
		err := json.Unmarshal(data, &e)
		return e, err
	}}
}

var codecs = func() map[string]codec {
	m := map[string]codec{}
	add := func(name string, c codec) { m[name] = c }
	add(register[domain.HumanApproved]())
	add(register[domain.SpecgatePassed]())
	add(register[domain.SpecgateFailed]())
	add(register[domain.SpecRejected]())
	add(register[domain.GatePassed]())
	add(register[domain.GateFailed]())
	add(register[domain.StageEntered]())
	add(register[domain.ReviewPassed]())
	add(register[domain.ChangesRequested]())
	add(register[domain.RoundStarted]())
	add(register[domain.NeedsHuman]())
	add(register[domain.TriageRequested]())
	add(register[domain.TriageRetried]())
	add(register[domain.MergeFailed]())
	add(register[domain.BudgetExhausted]())
	add(register[domain.ActionFailed]())
	add(register[domain.NeedsInfo]())
	add(register[domain.JobRejected]())
	add(register[domain.MergeRequested]())
	add(register[domain.MergeCompleted]())
	add(register[domain.MessageReceived]())
	add(register[domain.ReworkRequested]())
	add(register[domain.InfoAnswered]())
	add(register[domain.OverrideRequested]())
	add(register[domain.MergeOverridden]())
	add(register[domain.LimitOverrideRequested]())
	add(register[domain.LimitOverridden]())
	add(register[domain.CapRaiseRequested]())
	add(register[domain.CapRaised]())
	add(register[domain.StopRequested]())
	add(register[domain.JobStopped]())
	add(register[domain.ReplanRequested]())
	add(register[domain.SliceDropped]())
	add(register[domain.ReplanSubmitted]())
	add(register[domain.CheckpointReached]())
	add(register[domain.NextSliceRequested]())
	add(register[domain.SliceStarted]())
	add(register[domain.InitiativeEnded]())
	add(register[domain.InitiativeNeedsHumanRaised]())
	add(register[domain.InitiativeAnswered]())
	// Result is an interface, so ResultSubmitted carries its kind to decode.
	m[domain.ResultSubmitted{}.EventName()] = codec{reflect.TypeFor[domain.ResultSubmitted](), decodeResultSubmitted}
	return m
}()

// Encode puts ev in an envelope. An event whose type is not a known domain
// event is an ErrUnknownEvent error.
func Encode(ev domain.Event) (Envelope, error) {
	if ev == nil {
		return Envelope{}, fmt.Errorf("eventjson: no event: %w", ports.ErrUnknownEvent)
	}
	c, ok := codecs[ev.EventName()]
	if !ok || reflect.TypeOf(ev) != c.typ {
		return Envelope{}, fmt.Errorf("eventjson: %s (%T): %w", ev.EventName(), ev, ports.ErrUnknownEvent)
	}
	var data []byte
	var err error
	if e, isResult := ev.(domain.ResultSubmitted); isResult {
		data, err = encodeResultSubmitted(e)
	} else {
		data, err = json.Marshal(ev)
	}
	if err != nil {
		return Envelope{}, fmt.Errorf("eventjson: encode %s: %w", ev.EventName(), err)
	}
	var at struct {
		At time.Time `json:"at"`
	}
	if err := json.Unmarshal(data, &at); err != nil {
		return Envelope{}, fmt.Errorf("eventjson: time of %s: %w", ev.EventName(), err)
	}
	return Envelope{Name: ev.EventName(), At: at.At, Data: data}, nil
}

// Decode returns the concrete event in env. A name that is not a known domain
// event is an ErrUnknownEvent error, never an empty event.
func Decode(env Envelope) (domain.Event, error) {
	c, ok := codecs[env.Name]
	if !ok {
		return nil, fmt.Errorf("eventjson: %q: %w", env.Name, ports.ErrUnknownEvent)
	}
	ev, err := c.decode(env.Data)
	if err != nil {
		return nil, fmt.Errorf("eventjson: decode %s: %w", env.Name, err)
	}
	return ev, nil
}
