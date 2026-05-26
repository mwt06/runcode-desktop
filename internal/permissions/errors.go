package permissions

import "errors"

var (
	ErrInvalidInput  = errors.New("invalid permission input")
	ErrInvalidTarget = errors.New("invalid permission target")
)
