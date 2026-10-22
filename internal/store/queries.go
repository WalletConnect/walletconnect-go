package store

import "strings"

// Topics is the resubscribe set: live pairings, pending sessions and sessions.
func (t *Tx) Topics() ([]string, error) {
	pairings, err := t.Pairings()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range pairings {
		if !p.Retired {
			out = append(out, p.Topic)
		}
	}
	for _, prefix := range []string{prefixPending, prefixSession} {
		if err := t.tx.Scan(prefix, func(k string, _ []byte) error {
			out = append(out, strings.TrimPrefix(k, prefix))
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SymKey resolves the decryption key for a topic from its session, pending
// session or pairing, in that order. It returns "" if the topic is unknown.
func (t *Tx) SymKey(topic string) (string, error) {
	s, err := t.Session(topic)
	if err != nil {
		return "", err
	}
	if s != nil {
		return s.SymKey, nil
	}
	ps, err := t.PendingSession(topic)
	if err != nil {
		return "", err
	}
	if ps != nil {
		return ps.SymKey, nil
	}
	p, err := t.Pairing(topic)
	if err != nil || p == nil || p.Retired {
		return "", err
	}
	return p.SymKey, nil
}

// NextExpiry returns the earliest expiry (unix seconds) of any record, or 0.
func (t *Tx) NextExpiry() (int64, error) {
	var next int64
	consider := func(ts int64) {
		if ts > 0 && (next == 0 || ts < next) {
			next = ts
		}
	}
	pairings, err := t.Pairings()
	if err != nil {
		return 0, err
	}
	for _, p := range pairings {
		consider(p.Expiry)
	}
	proposals, err := t.Proposals()
	if err != nil {
		return 0, err
	}
	for _, p := range proposals {
		consider(p.ExpiryTimestamp)
	}
	pending, err := t.PendingSessions()
	if err != nil {
		return 0, err
	}
	for _, p := range pending {
		consider(p.CreatedAt + PendingSessionTTL)
	}
	sessions, err := t.Sessions()
	if err != nil {
		return 0, err
	}
	for _, s := range sessions {
		consider(s.Expiry)
	}
	reqs, err := t.Requests()
	if err != nil {
		return 0, err
	}
	for _, r := range reqs {
		consider(r.ExpiryTimestamp)
	}
	return next, nil
}
