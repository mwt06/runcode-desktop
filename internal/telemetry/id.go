package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var fallbackIDCounter atomic.Uint64

func NewTraceID() string {
	return newID("trace")
}

func NewTurnID() string {
	return newID("turn")
}

func NewRequestID() string {
	return newID("req")
}

func newID(prefix string) string {
	var data [8]byte
	if _, err := rand.Read(data[:]); err == nil {
		return prefix + "_" + hex.EncodeToString(data[:])
	}
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), fallbackIDCounter.Add(1))
}
