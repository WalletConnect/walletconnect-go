package store

import "github.com/WalletConnect/walletconnect-go/internal/types"

func (t *Tx) PutRequest(r types.PendingRequest) error { return t.put(idKey(prefixRequest, r.ID), r) }

func (t *Tx) Request(id int64) (*types.PendingRequest, error) {
	return getPtr[types.PendingRequest](t, idKey(prefixRequest, id))
}

func (t *Tx) DeleteRequest(id int64) error { return t.tx.Delete(idKey(prefixRequest, id)) }

func (t *Tx) Requests() ([]types.PendingRequest, error) {
	return scan[types.PendingRequest](t, prefixRequest)
}
