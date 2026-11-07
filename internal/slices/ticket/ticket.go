package ticket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// ErrNotTicket means the trigger is neither a command nor a labeled issue,
// the only two ways to ask for work: free text never starts any. Callers
// compare with errors.Is.
var ErrNotTicket = errors.New("not a ticket")

// Trigger kinds that ask for work.
const (
	KindCommand = "command" // an explicit command, such as /ticket
	KindIssue   = "issue"   // an issue with the factory's label
)

// Trigger is one request as a provider hands it: the design's Trigger
// (03-integraciones.md) plus ID, the provider's own id of the request, which
// the design's lacks and which tells a repeat from a new request.
type Trigger struct {
	Provider string
	Thread   string
	// Actor is the author's id in Provider; it is never mapped to another
	// provider's.
	Actor string
	Kind  string
	ID    string
	Text  string
	At    time.Time
}

// Allowlist is, per provider, the ids of the people who may ask for work.
type Allowlist map[string][]string

// allows says whether author may ask for work through provider. An id on
// another provider's list does not count.
func (a Allowlist) allows(provider, author string) bool {
	return slices.Contains(a[provider], author)
}

// Outcome is what a trigger came to.
type Outcome string

// Outcomes.
const (
	// Created means a new job was saved with its confirmation.
	Created Outcome = "created"
	// Repeated means the request had a job already: nothing new was saved.
	Repeated Outcome = "repeated"
	// Discarded means the author is not allowed: no job and no reply, only
	// the discard log's entry.
	Discarded Outcome = "discarded"
)

// Result is what Handle did with a trigger. Job is empty when Discarded.
type Result struct {
	Outcome Outcome
	Job     domain.JobID
}

// ActionReply is the kind of the confirmation action: a reply in a thread.
const ActionReply = "reply"

// Reply is the confirmation's payload.
type Reply struct {
	Provider string `json:"provider"`
	Thread   string `json:"thread"`
	Text     string `json:"text"`
}

// maxTitle caps the title taken from the request's first line, in runes.
const maxTitle = 80

// Handler turns triggers into jobs.
type Handler struct {
	Jobs     jobs
	Discards discards
	Allow    Allowlist
	// Pipeline is the flow new jobs follow: they start in its first stage.
	Pipeline domain.Pipeline
}

// Handle creates t's job and its confirmation, both or neither, when t's
// author is on its provider's allowlist; a request that has a job already is
// Repeated, so a trigger handed twice, even at once, leaves one job and one
// confirmation. An author not on it gets nothing back and t is kept as
// discarded, without its text. A trigger missing its provider, thread,
// author or id cannot be told from another and is refused with
// domain.ErrMissingField; one of another kind with ErrNotTicket. Neither
// saves anything.
func (h *Handler) Handle(ctx context.Context, t Trigger) (Result, error) {
	if err := check(t); err != nil {
		return Result{}, fmt.Errorf("ticket: %w", err)
	}
	if !h.Allow.allows(t.Provider, t.Actor) {
		d := ports.Discarded{Provider: t.Provider, Thread: t.Thread, Request: t.ID, Author: t.Actor, At: t.At}
		if err := h.Discards.Discard(ctx, d); err != nil {
			return Result{}, fmt.Errorf("ticket: %w", err)
		}
		return Result{Outcome: Discarded}, nil
	}
	if len(h.Pipeline.Stages) == 0 {
		return Result{}, fmt.Errorf("ticket: pipeline %q without stages: %w", h.Pipeline.Name, domain.ErrMissingField)
	}
	job := domain.Job{
		ID:       JobID(t),
		Title:    title(t.Text),
		Pipeline: h.Pipeline.Name,
		Stage:    h.Pipeline.Stages[0].Name,
		Round:    1,
		Origin:   domain.Origin{Provider: t.Provider, Thread: t.Thread, Author: t.Actor},
		Request:  t.Text,
	}
	confirm, err := confirmation(job)
	if err != nil {
		return Result{}, fmt.Errorf("ticket: %w", err)
	}
	err = h.Jobs.Create(ctx, job, confirm)
	if errors.Is(err, ports.ErrJobExists) {
		return Result{Outcome: Repeated, Job: job.ID}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("ticket: %w", err)
	}
	return Result{Outcome: Created, Job: job.ID}, nil
}

// check refuses a trigger that does not ask for work or could not be told
// from another.
func check(t Trigger) error {
	for _, f := range []struct{ name, value string }{
		{"provider", t.Provider}, {"thread", t.Thread}, {"actor", t.Actor}, {"id", t.ID},
	} {
		if f.value == "" {
			return fmt.Errorf("trigger without %s: %w", f.name, domain.ErrMissingField)
		}
	}
	if t.Kind != KindCommand && t.Kind != KindIssue {
		return fmt.Errorf("trigger of kind %q: %w", t.Kind, ErrNotTicket)
	}
	return nil
}

// JobID is the job a request gets: the same provider, thread and id always
// give the same one, so the store's refusal of a second job with it is what
// stops a repeat. Each part is length-prefixed, so no two different
// requests hash the same text.
func JobID(t Trigger) domain.JobID {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%s%d:%s%d:%s", len(t.Provider), t.Provider, len(t.Thread), t.Thread, len(t.ID), t.ID))
	return domain.JobID("ticket-" + hex.EncodeToString(sum[:8]))
}

// confirmation is the reply that tells the thread its job exists. Its key
// is the job's, so it is asked for once per job.
func confirmation(job domain.Job) (ports.Action, error) {
	payload, err := json.Marshal(Reply{
		Provider: job.Origin.Provider,
		Thread:   job.Origin.Thread,
		Text:     fmt.Sprintf("Job %s created.", job.ID),
	})
	if err != nil {
		return ports.Action{}, fmt.Errorf("encode confirmation of %q: %w", job.ID, err)
	}
	return ports.Action{Key: string(job.ID) + "/ticket/confirm", Kind: ActionReply, Payload: string(payload)}, nil
}

// title is the request's first non-blank line, cut to maxTitle runes.
func title(text string) string {
	for _, line := range lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > maxTitle {
			return string(r[:maxTitle-1]) + "…"
		}
		return line
	}
	return ""
}
