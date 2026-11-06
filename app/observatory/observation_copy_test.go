package observatory

import (
	"context"
	"sync"
	"testing"
)

// Core spec007 FR-005: GetObservation hands out copies taken under the lock, so a reader never sees
// the prober updating a status in place (run with -race).
func TestGetObservationCopiesUnderTheLock(t *testing.T) {
	o := &Observer{}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 500 {
			o.updateStatusForResult("a", &ProbeResult{Alive: i%2 == 0, Delay: int64(i)})
		}
	})
	wg.Go(func() {
		for range 500 {
			result, _ := o.GetObservation(context.Background())
			for _, status := range result.(*ObservationResult).Status {
				_, _ = status.Alive, status.Delay
			}
		}
	})
	wg.Wait()
	result, _ := o.GetObservation(context.Background())
	result.(*ObservationResult).Status[0].Alive = !result.(*ObservationResult).Status[0].Alive
	if again, _ := o.GetObservation(context.Background()); again.(*ObservationResult).Status[0].Alive == result.(*ObservationResult).Status[0].Alive {
		t.Fatal("a reader changed the observatory's own status")
	}
}
