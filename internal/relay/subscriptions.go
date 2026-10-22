package relay

import (
	"encoding/json"
	"strconv"
)

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
