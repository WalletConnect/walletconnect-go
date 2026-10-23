package relay

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
)

// batchSubscribeLimit is the most topics the relay accepts in one
// irn_batchSubscribe; it rejects a larger batch outright. The JS SDK splits at
// the same size (subscriber.batchSubscribeTopicsLimit).
const batchSubscribeLimit = 500

// batchSubscribeCalls splits topics into irn_batchSubscribe calls of at most
// batchSubscribeLimit topics each, in the order given. Each call carries its
// own copy of the topics, and a batch the relay rejects is retried on its own
// through subscribeFailed, so one bad batch does not hold up the others.
func batchSubscribeCalls(ctx context.Context, topics []string) []*call {
	calls := make([]*call, 0, (len(topics)+batchSubscribeLimit-1)/batchSubscribeLimit)
	for start := 0; start < len(topics); start += batchSubscribeLimit {
		batch := slices.Clone(topics[start:min(start+batchSubscribeLimit, len(topics))])
		calls = append(calls, &call{ctx: ctx, op: opBatchSubscribe, method: "irn_batchSubscribe",
			params: map[string]any{"topics": batch}, topics: batch})
	}
	return calls
}

// Subscriptions are tracked per connection so a topic is subscribed and
// unsubscribed at most once on a socket. The relay gives each subscribe its
// own subscription ID, so a second subscribe for the same topic makes it
// deliver every mailbox message twice.

// claimSubscribe reports whether a subscribe call must be sent. A topic
// already subscribed on this socket is answered locally.
func (r *runner) claimSubscribe(cl *call) bool {
	if r.subs[cl.topic] {
		r.finish(cl, json.RawMessage(strconv.Quote(r.c.SubscriptionID(cl.topic))), nil)
		return false
	}
	r.subs[cl.topic] = true
	return true
}

// claimUnsubscribe reports whether an unsubscribe call must be sent. A topic
// not subscribed on this socket is answered locally.
func (r *runner) claimUnsubscribe(cl *call) bool {
	if !r.subs[cl.topic] {
		r.finish(cl, json.RawMessage("true"), nil)
		return false
	}
	delete(r.subs, cl.topic)
	return true
}

// subscribeFailed forgets a subscribe the relay rejected, so a retry is sent.
func (r *runner) subscribeFailed(cl *call) {
	switch cl.op {
	case opSubscribe:
		delete(r.subs, cl.topic)
	case opBatchSubscribe:
		for _, t := range cl.topics {
			delete(r.subs, t)
		}
	}
}
