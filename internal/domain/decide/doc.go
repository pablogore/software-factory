// Package decide is the generic state machine: Decide(job, event, pipeline)
// returns the events that follow. It is a pure function; slices persist the
// events and run the effects through ports.
package decide
