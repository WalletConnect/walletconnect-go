package jsonrpc

import (
	"crypto/rand"
	"math/big"
	"time"
)

// PayloadID is the peer-to-peer request ID: unix millis * 1000 + 3 random digits.
func PayloadID() int64 {
	return time.Now().UnixMilli()*1000 + randomBelow(1000)
}

// RelayID is the relay request ID: unix millis * 1e6 + 6 random digits.
func RelayID() int64 {
	return time.Now().UnixMilli()*1_000_000 + randomBelow(1_000_000)
}

func randomBelow(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0
	}
	return v.Int64()
}
