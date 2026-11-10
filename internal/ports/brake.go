package ports

import "context"

// BrakeLog keeps whether the run's brake between issues is on. It lives in
// factoryd's store, so a restart finds the brake as it was left and starts
// no new issue only because factoryd started again.
type BrakeLog interface {
	// SaveBrake saves whether the brake is on, replacing what was saved.
	SaveBrake(ctx context.Context, on bool) error
	// BrakeOn is what SaveBrake saved last; false when it never ran.
	BrakeOn(ctx context.Context) (bool, error)
}
