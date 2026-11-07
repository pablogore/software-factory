package domain

// IssueCap is a cap of an issue: one that stopped it from starting another
// slice, or the budget that stopped one of its jobs; a person may raise any of
// them (CapRaiseRequested). It is a closed set of stable codes, never text for
// a person: each channel shows it in its own words, in the configured
// language.
type IssueCap string

// The caps per issue.
const (
	CapSlices IssueCap = "issue-slices" // the issue has as many slices as the cap allows
	CapUSD    IssueCap = "issue-usd"    // the issue's jobs cost more USD than the cap allows
	CapBudget IssueCap = "job-budget"   // the job spent its whole budget
)

var issueCaps = []IssueCap{CapSlices, CapUSD, CapBudget}

// IssueCaps lists every cap. It returns a copy, so changing it changes
// neither the set nor what Validate accepts.
func IssueCaps() []IssueCap {
	return append([]IssueCap(nil), issueCaps...)
}

// Validate returns an error when c is not one of IssueCaps.
func (c IssueCap) Validate() error {
	return oneOf("issue cap", c, issueCaps...)
}
