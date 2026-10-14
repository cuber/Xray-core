package anytls

import "time"

type idleSnapshot struct {
	streams int
	count   uint64
}

// Like common/mux, an empty session is idle only if no stream was created
// between observations. Transport reads, padding and heartbeats do not count.
func (s *Server) retireIfIdle(c *connection, previous *idleSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || c.retiring || c.ctx.Err() != nil {
		return false
	}
	idle := previous.streams == 0 && c.streams == 0 && previous.count == c.streamCount
	*previous = idleSnapshot{streams: c.streams, count: c.streamCount}
	if idle {
		// Block new streams before releasing the lock, but return credits only
		// after the protocol loop and all stream handlers actually exit.
		c.retiring = true
	}
	return idle
}

func (s *Server) monitorIdle(c *connection, ticks <-chan time.Time) {
	s.mu.Lock()
	previous := idleSnapshot{streams: c.streams, count: c.streamCount}
	s.mu.Unlock()
	for {
		select {
		case <-c.ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
			if s.retireIfIdle(c, &previous) {
				c.close()
				return
			}
		}
	}
}
