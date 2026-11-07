// Package ticket is the ticket slice: it turns a request, a command or an
// issue with the factory's label, into one job (03-integraciones.md, "Reglas
// de entrada"). Only an author on the allowlist of the request's own
// provider starts work; anyone else's request creates nothing and is kept as
// discarded. The same request handed twice, even at once, leaves one job and
// one confirmation in its thread. The request's text reaches agents only
// quoted, as data (Brief).
//
// No provider hands it requests yet: the EventSource ports are E06's, so
// Trigger is the slice's own entry with the shape of the design's Trigger.
package ticket
