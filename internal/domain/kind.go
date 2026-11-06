package domain

import "fmt"

// Kind is a role type programmed in Go: it fixes what a role receives, the
// schema of the result it submits and how the flow reacts to it (04-roles.md).
type Kind string

// The six kinds. A new kind needs Go code and a schema.
const (
	KindIntake  Kind = "intake"
	KindSpec    Kind = "spec"
	KindBuild   Kind = "build"
	KindReview  Kind = "review"
	KindApprove Kind = "approve"
	KindTask    Kind = "task"
)

// Kinds lists every kind in pipeline order.
var Kinds = []Kind{KindIntake, KindSpec, KindBuild, KindReview, KindApprove, KindTask}

// Validate returns an error when k is not one of Kinds.
func (k Kind) Validate() error {
	for _, known := range Kinds {
		if k == known {
			return nil
		}
	}
	return fmt.Errorf("%w: kind %q", ErrInvalidValue, k)
}

// DefaultRole is the role a stage of kind k takes when it lists none, named
// like the current factory's agents. Review and task have no default: a
// review lists its reviewers and a task is always a role of its own.
func DefaultRole(k Kind) (string, bool) {
	switch k {
	case KindIntake:
		return "intake", true
	case KindSpec:
		return "spec-writer", true
	case KindBuild:
		return "builder", true
	case KindApprove:
		return "approver", true
	default:
		return "", false
	}
}
