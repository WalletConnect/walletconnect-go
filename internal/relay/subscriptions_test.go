package relay

import (
	"context"
	"fmt"
	"testing"
)

func topicsN(n int) []string {
	t := make([]string, n)
	for i := range t {
		t[i] = fmt.Sprintf("topic-%04d", i)
	}
	return t
}

// The relay rejects an irn_batchSubscribe with more than batchSubscribeLimit
// topics, so a client with many sessions must resubscribe in several calls.
func TestBatchSubscribeCallsSplitAtLimit(t *testing.T) {
	cases := []struct {
		topics int
		sizes  []int
	}{
		{0, nil},
		{1, []int{1}},
		{batchSubscribeLimit, []int{batchSubscribeLimit}},
		{batchSubscribeLimit + 1, []int{batchSubscribeLimit, 1}},
		{2*batchSubscribeLimit + 250, []int{batchSubscribeLimit, batchSubscribeLimit, 250}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.topics), func(t *testing.T) {
			topics := topicsN(tc.topics)
			calls := batchSubscribeCalls(context.Background(), topics)
			if len(calls) != len(tc.sizes) {
				t.Fatalf("%d topics: got %d calls, want %d", tc.topics, len(calls), len(tc.sizes))
			}
			next := 0
			for i, cl := range calls {
				if cl.op != opBatchSubscribe || cl.method != "irn_batchSubscribe" {
					t.Fatalf("call %d: op=%v method=%q", i, cl.op, cl.method)
				}
				if len(cl.topics) != tc.sizes[i] {
					t.Fatalf("call %d: %d topics, want %d", i, len(cl.topics), tc.sizes[i])
				}
				params, ok := cl.params.(map[string]any)
				if !ok || len(params["topics"].([]string)) != len(cl.topics) {
					t.Fatalf("call %d: params %v do not match topics", i, cl.params)
				}
				for j, topic := range cl.topics {
					if topic != topics[next] || params["topics"].([]string)[j] != topic {
						t.Fatalf("call %d topic %d: got %q, want %q", i, j, topic, topics[next])
					}
					next++
				}
			}
			if next != tc.topics {
				t.Fatalf("covered %d of %d topics", next, tc.topics)
			}
		})
	}
}

// Each batch owns its topics, so a rejected batch forgets only its own and the
// other batches stay subscribed.
func TestBatchSubscribeFailedForgetsOnlyThatBatch(t *testing.T) {
	topics := topicsN(batchSubscribeLimit + 2)
	calls := batchSubscribeCalls(context.Background(), topics)
	r := &runner{subs: map[string]bool{}}
	for _, topic := range topics {
		r.subs[topic] = true
	}
	r.subscribeFailed(calls[1])
	if len(r.subs) != batchSubscribeLimit {
		t.Fatalf("%d topics still subscribed, want %d", len(r.subs), batchSubscribeLimit)
	}
	for _, topic := range calls[1].topics {
		if r.subs[topic] {
			t.Fatalf("%s still subscribed after its batch failed", topic)
		}
	}
	topics[0] = "mutated"
	if calls[0].topics[0] == "mutated" {
		t.Fatal("batch shares the caller's slice")
	}
}
