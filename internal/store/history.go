package store

import (
	"encoding/json"
	"strings"
)

// InsertHistory records h unless its ID exists, and reports whether it did.
func (t *Tx) InsertHistory(h History) (bool, error) {
	existing, err := t.History(h.ID)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, nil
	}
	if err := t.put(idKey(prefixHistory, h.ID), h); err != nil {
		return false, err
	}
	return true, t.tx.Put(idKey(prefixHistoryTopic+h.Topic+"/", h.ID), nil)
}

func (t *Tx) History(id int64) (*History, error) {
	return getPtr[History](t, idKey(prefixHistory, id))
}

func (t *Tx) SetHistoryResponse(id int64, response json.RawMessage) error {
	h, err := t.History(id)
	if err != nil || h == nil {
		return err
	}
	h.Response = response
	return t.put(idKey(prefixHistory, id), h)
}

func (t *Tx) DeleteHistoryByTopic(topic string) error {
	prefix := prefixHistoryTopic + topic + "/"
	var ids []string
	if err := t.tx.Scan(prefix, func(k string, _ []byte) error {
		ids = append(ids, strings.TrimPrefix(k, prefix))
		return nil
	}); err != nil {
		return err
	}
	for _, id := range ids {
		if err := t.tx.Delete(prefixHistory + id); err != nil {
			return err
		}
		if err := t.tx.Delete(prefix + id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteHistoryBefore removes history records created before ts.
func (t *Tx) DeleteHistoryBefore(ts int64) error {
	old, err := scan[History](t, prefixHistory)
	if err != nil {
		return err
	}
	for _, h := range old {
		if h.CreatedAt >= ts {
			continue
		}
		if err := t.tx.Delete(idKey(prefixHistory, h.ID)); err != nil {
			return err
		}
		if err := t.tx.Delete(idKey(prefixHistoryTopic+h.Topic+"/", h.ID)); err != nil {
			return err
		}
	}
	return nil
}
