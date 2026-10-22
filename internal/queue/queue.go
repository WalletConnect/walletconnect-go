// Package queue is an unbounded FIFO whose Push never blocks. Producers that
// must not stall, such as the relay goroutine, hand work to a consumer
// goroutine through it.
package queue

import "sync"

type Queue[T any] struct {
	mu     sync.Mutex
	items  []T
	signal chan struct{}
}

func New[T any]() *Queue[T] { return &Queue[T]{signal: make(chan struct{}, 1)} }

func (q *Queue[T]) Push(v T) {
	q.mu.Lock()
	q.items = append(q.items, v)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// Signal receives a value after one or more pushes. Drain with PopAll.
func (q *Queue[T]) Signal() <-chan struct{} { return q.signal }

// PopAll removes and returns every queued item in push order.
func (q *Queue[T]) PopAll() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

// Len reports how many items are queued.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
