package domain

// NextReason is why a run's summary recommends the issue it does, or why it
// recommends none. It is a closed set of stable codes, never text for a
// person: each channel shows it in its own words, in the configured
// language.
type NextReason string

// The reasons for a recommended issue, one per rule the queue orders by.
const (
	NextTechLead NextReason = "tech-lead-order"        // first in the tech lead's order
	NextPriority NextReason = "product-owner-priority" // first in the product owner's priority
	NextLowest   NextReason = "lowest-number"          // no order lists it; lowest number waiting
)

// The reasons for recommending no issue.
const (
	NoNextEmpty NextReason = "queue-empty" // no issue waits
	NoNextHeld  NextReason = "all-held"    // every waiting issue is held by its dependencies
)

var nextReasons = []NextReason{NextTechLead, NextPriority, NextLowest, NoNextEmpty, NoNextHeld}

// NextReasons lists every reason. It returns a copy, so changing it changes
// neither the set nor what Validate accepts.
func NextReasons() []NextReason {
	return append([]NextReason(nil), nextReasons...)
}

// Validate returns an error when r is not one of NextReasons.
func (r NextReason) Validate() error {
	return oneOf("next reason", r, nextReasons...)
}
