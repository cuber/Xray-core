package core

// OnClose registers synchronous retirement of an instance-owned resource.
// Hooks must not call lifecycle methods on the instance. False means shutdown
// already began and the caller must not publish or start the resource.
func (s *Instance) OnClose(closeOwned func()) bool {
	s.retirementLock.Lock()
	defer s.retirementLock.Unlock()
	if s.retired {
		return false
	}
	s.retirementHooks = append(s.retirementHooks, closeOwned)
	return true
}
