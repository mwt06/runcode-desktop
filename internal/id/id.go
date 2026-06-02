package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var fallbackCounter atomic.Uint64

func New(prefix string) string {
	var data [8]byte
	if _, err := rand.Read(data[:]); err == nil {
		return prefix + "_" + hex.EncodeToString(data[:])
	}
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), fallbackCounter.Add(1))
}
