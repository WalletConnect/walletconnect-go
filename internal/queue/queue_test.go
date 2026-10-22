package queue

import "testing"

func TestQueue(t *testing.T) {
	q := New[int]()
	q.Push(1)
	q.Push(2)
	<-q.Signal()
	if got := q.PopAll(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("PopAll = %v", got)
	}
	if got := q.PopAll(); len(got) != 0 {
		t.Fatalf("PopAll after drain = %v", got)
	}
	select {
	case <-q.Signal():
		t.Fatal("signal without a push")
	default:
	}
}
