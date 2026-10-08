package main

// stateSnapshot returns the state as of the last processed command. Test-only:
// production code never reads state off the loop. Because do() waits for its
// reply, a stateSnapshot() call after a do() always observes that command.
func (s *supervisor) stateSnapshot() supervisorState {
	return supervisorState(s.stateSnap.Load())
}
