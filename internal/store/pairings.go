package store

func (t *Tx) PutPairing(p Pairing) error { return t.put(prefixPairing+p.Topic, p) }

func (t *Tx) Pairing(topic string) (*Pairing, error) {
	return getPtr[Pairing](t, prefixPairing+topic)
}

func (t *Tx) DeletePairing(topic string) error { return t.tx.Delete(prefixPairing + topic) }

func (t *Tx) Pairings() ([]Pairing, error) { return scan[Pairing](t, prefixPairing) }

// ForgetPairing deletes a pairing, the proposal sent or received on it (if
// proposalID is not 0) and the pairing topic's history.
func (t *Tx) ForgetPairing(topic string, proposalID int64) error {
	if proposalID != 0 {
		if err := t.DeleteProposal(proposalID); err != nil {
			return err
		}
	}
	if err := t.DeletePairing(topic); err != nil {
		return err
	}
	return t.DeleteHistoryByTopic(topic)
}

// RetirePairing deletes the proposal answered on a wallet pairing and the
// pairing topic's history, and marks the pairing retired.
func (t *Tx) RetirePairing(topic string, proposalID int64) error {
	p, err := t.Pairing(topic)
	if err != nil || p == nil {
		return err
	}
	if err := t.DeleteProposal(proposalID); err != nil {
		return err
	}
	if err := t.DeleteHistoryByTopic(topic); err != nil {
		return err
	}
	p.Retired = true
	return t.PutPairing(*p)
}
