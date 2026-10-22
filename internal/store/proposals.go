package store

func (t *Tx) PutProposal(p ProposalRecord) error { return t.put(idKey(prefixProposal, p.ID), p) }

func (t *Tx) Proposal(id int64) (*ProposalRecord, error) {
	return getPtr[ProposalRecord](t, idKey(prefixProposal, id))
}

func (t *Tx) DeleteProposal(id int64) error { return t.tx.Delete(idKey(prefixProposal, id)) }

func (t *Tx) Proposals() ([]ProposalRecord, error) {
	return scan[ProposalRecord](t, prefixProposal)
}
