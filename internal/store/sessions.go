package store

func (t *Tx) PutPendingSession(p PendingSession) error {
	return t.put(prefixPending+p.SessionTopic, p)
}

func (t *Tx) PendingSession(topic string) (*PendingSession, error) {
	return getPtr[PendingSession](t, prefixPending+topic)
}

func (t *Tx) DeletePendingSession(topic string) error { return t.tx.Delete(prefixPending + topic) }

func (t *Tx) PendingSessions() ([]PendingSession, error) {
	return scan[PendingSession](t, prefixPending)
}

func (t *Tx) PutSession(s SessionRecord) error { return t.put(prefixSession+s.Topic, s) }

func (t *Tx) Session(topic string) (*SessionRecord, error) {
	return getPtr[SessionRecord](t, prefixSession+topic)
}

func (t *Tx) Sessions() ([]SessionRecord, error) { return scan[SessionRecord](t, prefixSession) }

// DeleteSession removes the session, its pending requests and its history.
func (t *Tx) DeleteSession(topic string) error {
	if err := t.tx.Delete(prefixSession + topic); err != nil {
		return err
	}
	reqs, err := t.Requests()
	if err != nil {
		return err
	}
	for _, r := range reqs {
		if r.Topic == topic {
			if err := t.DeleteRequest(r.ID); err != nil {
				return err
			}
		}
	}
	return t.DeleteHistoryByTopic(topic)
}
