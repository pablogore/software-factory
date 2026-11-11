package wiring

import (
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/spec"
)

// SpecgateSubscriber is the name the bus keeps specgate's runs under.
const SpecgateSubscriber = "spec.specgate"

// Specgate is specgate over jobs, checking the spec each job's history has
// last.
func Specgate(jobs WorkerStore, p Pipelines, clock ports.Clock) *spec.Specgate {
	return &spec.Specgate{Jobs: jobs, History: jobs, Pipelines: p, Clock: clock}
}

// SubscribeSpecgate has b hand g every job entering specgate, so a spec is
// checked as soon as its writer hands it in. Call it before b starts.
func SubscribeSpecgate(b *bus.Bus, g *spec.Specgate) error {
	return b.Subscribe(SpecgateSubscriber, g.Handle, domain.StageEntered{}.EventName())
}
