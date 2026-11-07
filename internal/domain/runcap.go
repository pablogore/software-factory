package domain

// RunCap is the cap of a run that stopped it from starting new issues. It is
// a closed set of stable codes, never text for a person: each channel shows
// it in its own words, in the configured language.
type RunCap string

// The caps of a run.
const (
	CapRunIssues RunCap = "run-issues" // the run finished as many issues as the cap allows
	CapRunUSD    RunCap = "run-usd"    // the run spent as many USD as the cap allows
)

var runCaps = []RunCap{CapRunIssues, CapRunUSD}

// RunCaps lists every cap of a run. It returns a copy, so changing it
// changes neither the set nor what Validate accepts.
func RunCaps() []RunCap {
	return append([]RunCap(nil), runCaps...)
}

// Validate returns an error when c is not one of RunCaps.
func (c RunCap) Validate() error {
	return oneOf("run cap", c, runCaps...)
}
