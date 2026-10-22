package engine

import "strings"

// outOfSync mirrors isRequestOutOfSync: compare IDs without the 3 random digits.
func outOfSync(last, current int64) bool { return current/1000 < last/1000 }

// inOrder reports whether id is not older than the last one seen for key.
func (e *Engine) inOrder(m map[string]int64, key string, id int64) bool {
	e.omu.Lock()
	defer e.omu.Unlock()
	last := m[key]
	return last == 0 || !outOfSync(last, id)
}

func (e *Engine) markSeen(m map[string]int64, key string, id int64) {
	e.omu.Lock()
	m[key] = id
	e.omu.Unlock()
}

func (e *Engine) forgetTopic(topic string) {
	e.omu.Lock()
	defer e.omu.Unlock()
	delete(e.lastUpdate, topic)
	for k := range e.lastEvent {
		if strings.HasPrefix(k, topic+"/") {
			delete(e.lastEvent, k)
		}
	}
}
