package memstore

import "context"

// SaveBrake implements ports.BrakeLog.
func (s *Store) SaveBrake(_ context.Context, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.brake = on
	return nil
}

// BrakeOn implements ports.BrakeLog.
func (s *Store) BrakeOn(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.brake, nil
}
