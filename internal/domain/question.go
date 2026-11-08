package domain

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// Audience is who a question an agent asks a person is for: the product
// person or the technical one. Channels will send it to the person
// configured for it (E06); for now it is only recorded.
type Audience string

// The two audiences.
const (
	AudienceProduct Audience = "product"
	AudienceTech    Audience = "tech"
)

// Validate returns an error when a is not one of the audiences.
func (a Audience) Validate() error {
	return oneOf("to", a, AudienceProduct, AudienceTech)
}

// Bounds of a question an agent asks with ask_human: enough for a closed
// question and its choices, short enough for a chat message.
const (
	MaxQuestionText = 2000
	MaxOptions      = 10
	MaxOptionText   = 200
)

// HumanAsked records that Role's agent asked a person Question, with the
// Options it may be answered with, if any, for To. The job then waits for
// the answer, which the stage that asked gets as input. Question and Options
// are agent-written text, data only: Decide never parses them or routes on
// them.
type HumanAsked struct {
	Job      JobID     `json:"job"`
	Role     string    `json:"role"`
	Question string    `json:"question"`
	Options  []string  `json:"options,omitempty"`
	To       Audience  `json:"to"`
	At       time.Time `json:"at"`
}

// EventName implements Event.
func (HumanAsked) EventName() string { return "HumanAsked" }

// JobID implements Event.
func (e HumanAsked) JobID() JobID { return e.Job }

// Validate refuses a question from a bad role name, a blank one or one over
// MaxQuestionText characters, more than MaxOptions options, a blank option
// or one over MaxOptionText characters, and an unknown audience.
func (e HumanAsked) Validate() error {
	if err := ValidateRoleName(e.Role); err != nil {
		return err
	}
	if Blank(e.Question) {
		return fmt.Errorf("%w: question", ErrMissingField)
	}
	if n := utf8.RuneCountInString(e.Question); n > MaxQuestionText {
		return fmt.Errorf("%w: question of %d characters, over %d", ErrInvalidValue, n, MaxQuestionText)
	}
	if len(e.Options) > MaxOptions {
		return fmt.Errorf("%w: %d options, over %d", ErrInvalidValue, len(e.Options), MaxOptions)
	}
	for i, o := range e.Options {
		if Blank(o) {
			return fmt.Errorf("%w: option %d is blank", ErrMissingField, i+1)
		}
		if n := utf8.RuneCountInString(o); n > MaxOptionText {
			return fmt.Errorf("%w: option %d of %d characters, over %d", ErrInvalidValue, i+1, n, MaxOptionText)
		}
	}
	return e.To.Validate()
}
