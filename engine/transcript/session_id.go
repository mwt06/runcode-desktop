package transcript

import (
	"errors"
	"strings"

	"github.com/wt68/runcode/engine/internal/id"
)

var ErrInvalidSessionID = errors.New("invalid session id")

func NewSessionID() string {
	return id.New("sess")
}

func ValidateSessionID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return ErrInvalidSessionID
	}
	if strings.Contains(id, "..") || strings.TrimSpace(id) != id {
		return ErrInvalidSessionID
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return ErrInvalidSessionID
		}
	}
	return nil
}
