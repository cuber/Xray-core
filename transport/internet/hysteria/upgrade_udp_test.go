package hysteria

import (
	"io"
	"sync"
	"testing"
	"time"
)

func TestUpgradeHy2UDPConcurrentWriteAndClose(t *testing.T) {
	for round := 0; round < 100; round++ {
		m := &udpSessionManager{m: make(map[uint32]*InterConn), udpIdleTimeout: time.Hour}
		c := &InterConn{id: 7, ch: make(chan []byte), time: time.Now(), write: func([]byte) error { return nil }}
		m.m[c.id] = c
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 100; i++ {
					c.Write(make([]byte, 8))
					m.cleanOnce()
				}
			}()
		}
		close(start)
		m.Lock()
		m.closed = true
		m.close(c)
		m.Unlock()
		wg.Wait()
		if !m.cleanOnce() || len(m.m) != 0 {
			t.Fatal("retired UDP manager retained sessions")
		}
		if _, err := c.Write(make([]byte, 8)); err != io.ErrClosedPipe {
			t.Fatalf("closed session write: %v", err)
		}
		if _, err := c.Read(make([]byte, 8)); err != io.EOF {
			t.Fatalf("closed session read: %v", err)
		}
	}
}
