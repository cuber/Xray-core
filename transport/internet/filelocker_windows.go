package internet

// Acquire lock
func (fl *FileLocker) Acquire() error {
	return nil
}

func (fl *FileLocker) TryAcquire() error {
	return fl.Acquire()
}

// Release lock
func (fl *FileLocker) Release() {
	return
}
