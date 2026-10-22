package engine

// Stats counts the engine's in-memory state. Every count returns to zero once
// all sessions and proposals are gone; tests use it to catch leaks.
type Stats struct {
	Waiters      int // requests awaiting a peer answer
	UpdateOrder  int // sessions with a remembered update ID
	EventOrder   int // (session, event name) pairs with a remembered event ID
	QueuedEvents int // incoming messages not yet processed
}

func (e *Engine) Stats() Stats {
	e.wmu.Lock()
	waiters := len(e.waiters)
	e.wmu.Unlock()
	e.omu.Lock()
	defer e.omu.Unlock()
	return Stats{Waiters: waiters, UpdateOrder: len(e.lastUpdate), EventOrder: len(e.lastEvent), QueuedEvents: e.incoming.Len()}
}
