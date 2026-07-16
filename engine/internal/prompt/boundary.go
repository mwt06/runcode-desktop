package prompt

import (
	"errors"
	"strings"
	"unicode"
)

// DynamicBoundary marks the split between static and dynamic system prompt sections.
const DynamicBoundary = "__RUNCODE_DYNAMIC_BOUNDARY__"

var (
	// ErrBoundaryNotFound indicates the input contains no boundary marker.
	ErrBoundaryNotFound = errors.New("prompt boundary not found")

	// ErrMultipleBoundaries indicates the input contains more than one boundary marker.
	ErrMultipleBoundaries = errors.New("multiple prompt boundaries found")
)

// HasBoundary reports whether the prelude contains the dynamic boundary marker.
func HasBoundary(prelude string) bool {
	return strings.Contains(prelude, DynamicBoundary)
}

// Split divides the prelude into static and dynamic sections using the boundary marker.
// The boundary marker itself is removed. Blank lines around the boundary are preserved.
// Returns ErrBoundaryNotFound if no boundary is present or ErrMultipleBoundaries if more than one exists.
func Split(prelude string) (static, dynamic string, err error) {
	parts := strings.SplitN(prelude, DynamicBoundary, 3)
	switch len(parts) {
	case 1:
		return "", "", ErrBoundaryNotFound
	case 2:
		return parts[0], parts[1], nil
	default:
		return "", "", ErrMultipleBoundaries
	}
}

// SplitAndTrim is like Split but also removes trailing whitespace from the static
// section and leading whitespace from the dynamic section.
func SplitAndTrim(prelude string) (static, dynamic string, err error) {
	static, dynamic, err = Split(prelude)
	if err != nil {
		return "", "", err
	}
	return strings.TrimRightFunc(static, unicode.IsSpace),
		strings.TrimLeftFunc(dynamic, unicode.IsSpace), nil
}

// InsertBoundary joins static and dynamic sections with the boundary marker on its own line.
func InsertBoundary(static, dynamic string) string {
	return static + "\n" + DynamicBoundary + "\n" + dynamic
}

// Validate checks whether the prelude has a valid boundary structure.
func Validate(prelude string) error {
	_, _, err := Split(prelude)
	return err
}
