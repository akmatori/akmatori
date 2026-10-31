package handlers

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCorrelationGate_OneAtATime(t *testing.T) {
	var g correlationGate
	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := g.enter("a")
			defer release()
			n := inside.Add(1)
			for {
				m := maxInside.Load()
				if n <= m || maxInside.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inside.Add(-1)
		}()
	}
	wg.Wait()
	if got := maxInside.Load(); got != 1 {
		t.Fatalf("max alerts inside the gate = %d, want 1", got)
	}
}

func TestCorrelationGate_ReleaseIsIdempotent(t *testing.T) {
	var g correlationGate
	release := g.enter("a")
	release()
	release() // must not free a slot held by the next alert

	second := g.enter("b")
	done := make(chan struct{})
	go func() {
		g.enter("c")()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("third alert entered while the second still holds the gate")
	case <-time.After(50 * time.Millisecond):
	}
	second()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("third alert did not enter after the second released the gate")
	}
}

func TestCorrelationGate_GoesOnWithoutGateAfterMaxWait(t *testing.T) {
	old := correlationGateMaxWait
	correlationGateMaxWait = 20 * time.Millisecond
	t.Cleanup(func() { correlationGateMaxWait = old })

	var g correlationGate
	holder := g.enter("stuck")
	defer holder()

	start := time.Now()
	release := g.enter("storm")
	if waited := time.Since(start); waited < 20*time.Millisecond {
		t.Fatalf("entered after %v, want a wait of at least the max wait", waited)
	}
	// The no-op release must not free the holder's slot.
	release()
	correlationGateMaxWait = time.Second
	done := make(chan struct{})
	go func() {
		g.enter("next")()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("no-op release freed the holder's slot")
	case <-time.After(50 * time.Millisecond):
	}
	holder()
	<-done
}
