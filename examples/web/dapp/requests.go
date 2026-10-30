package main

import (
	"slices"
	"sync"
	"time"
)

// requestEntry is one signing request the dApp sent and its outcome.
type requestEntry struct {
	LocalID int64         `json:"localId"`
	Topic   string        `json:"topic"`
	Method  string        `json:"method"`
	ChainID string        `json:"chainId"`
	Account string        `json:"account"`
	At      int64         `json:"at"`
	Pending bool          `json:"pending,omitempty"`
	Result  *signedResult `json:"result,omitempty"`
	Error   string        `json:"error,omitempty"`
	Code    int           `json:"code,omitempty"`
}

// requestLog keeps the latest requests per session in memory, so a reloaded
// page still shows them. Pending requests are always kept.
type requestLog struct {
	mu      sync.Mutex
	next    int64
	byTopic map[string][]*requestEntry
}

const requestsPerSession = 20

func newRequestLog() *requestLog { return &requestLog{byTopic: map[string][]*requestEntry{}} }

// start records a pending request and returns a copy of its entry.
func (l *requestLog) start(topic, method, chainID, account string) requestEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	e := &requestEntry{LocalID: l.next, Topic: topic, Method: method, ChainID: chainID, Account: account, At: time.Now().Unix(), Pending: true}
	list := append([]*requestEntry{e}, l.byTopic[topic]...)
	// Drop the oldest answered entries; a pending one waits for its answer.
	for i := len(list) - 1; i >= 0 && len(list) > requestsPerSession; i-- {
		if !list[i].Pending {
			list = slices.Delete(list, i, i+1)
		}
	}
	l.byTopic[topic] = list
	return *e
}

// finish records the outcome and returns a copy of the entry.
func (l *requestLog) finish(topic string, id int64, result *signedResult, errMsg string, code int) requestEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.byTopic[topic] {
		if e.LocalID == id {
			e.Pending, e.Result, e.Error, e.Code = false, result, errMsg, code
			return *e
		}
	}
	return requestEntry{LocalID: id, Topic: topic, Result: result, Error: errMsg, Code: code}
}

// prune drops the entries of sessions that no longer exist.
func (l *requestLog) prune(live map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for topic := range l.byTopic {
		if !live[topic] {
			delete(l.byTopic, topic)
		}
	}
}

// snapshot copies every session's entries, newest first.
func (l *requestLog) snapshot() map[string][]requestEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string][]requestEntry, len(l.byTopic))
	for topic, list := range l.byTopic {
		for _, e := range list {
			out[topic] = append(out[topic], *e)
		}
	}
	return out
}
