package signal

import "sync"

// Broadcast calls every subscribed function on Fire (Core spec027 TL-5). Fire runs the functions on
// the caller's goroutine without holding any lock of its own, so a subscriber may call back into the
// component that fired. A cancelled subscription is never called again once its cancel returns; a
// subscriber must not cancel itself from inside its own call.
type Broadcast struct {
	mu   sync.Mutex
	next uint64
	subs map[uint64]*subscription
}

type subscription struct {
	fn    func()
	calls sync.WaitGroup
}

// Subscribe registers fn and returns its cancel.
func (b *Broadcast) Subscribe(fn func()) (cancel func()) {
	s := &subscription{fn: fn}
	b.mu.Lock()
	if b.subs == nil {
		b.subs = make(map[uint64]*subscription)
	}
	id := b.next
	b.next++
	b.subs[id] = s
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			s.calls.Wait()
		})
	}
}

// Fire calls every current subscriber once.
func (b *Broadcast) Fire() {
	b.mu.Lock()
	if len(b.subs) == 0 {
		b.mu.Unlock()
		return
	}
	subs := make([]*subscription, 0, len(b.subs))
	for _, s := range b.subs {
		s.calls.Add(1)
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.fn()
		s.calls.Done()
	}
}
