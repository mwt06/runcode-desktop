package prompt

import (
	"errors"
	"strings"
	"testing"
)

func TestDynamicBoundaryConstant(t *testing.T) {
	t.Parallel()

	if DynamicBoundary == "" {
		t.Fatal("DynamicBoundary must not be empty")
	}
	if !strings.HasPrefix(DynamicBoundary, "__") {
		t.Fatal("DynamicBoundary should start with __")
	}
	if !strings.HasSuffix(DynamicBoundary, "__") {
		t.Fatal("DynamicBoundary should end with __")
	}
}

func TestHasBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"no boundary", "just a plain prompt", false},
		{"contains boundary", "before" + DynamicBoundary + "after", true},
		{"boundary only", DynamicBoundary, true},
		{"boundary at start", DynamicBoundary + "dynamic", true},
		{"boundary at end", "static" + DynamicBoundary, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := HasBoundary(tt.input); got != tt.want {
				t.Errorf("HasBoundary(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSplitNoBoundary(t *testing.T) {
	t.Parallel()

	_, _, err := Split("no boundary here")
	if !errors.Is(err, ErrBoundaryNotFound) {
		t.Errorf("Split no boundary: expected ErrBoundaryNotFound, got %v", err)
	}
}

func TestSplitEmpty(t *testing.T) {
	t.Parallel()

	_, _, err := Split("")
	if !errors.Is(err, ErrBoundaryNotFound) {
		t.Errorf("Split empty: expected ErrBoundaryNotFound, got %v", err)
	}
}

func TestSplitBoundaryOnly(t *testing.T) {
	t.Parallel()

	static, dynamic, err := Split(DynamicBoundary)
	if err != nil {
		t.Fatalf("Split boundary only: unexpected error %v", err)
	}
	if static != "" {
		t.Errorf("static = %q, want empty", static)
	}
	if dynamic != "" {
		t.Errorf("dynamic = %q, want empty", dynamic)
	}
}

func TestSplitBoundaryAtStart(t *testing.T) {
	t.Parallel()

	static, dynamic, err := Split(DynamicBoundary + "dynamic content")
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if static != "" {
		t.Errorf("static = %q, want empty", static)
	}
	if dynamic != "dynamic content" {
		t.Errorf("dynamic = %q, want %q", dynamic, "dynamic content")
	}
}

func TestSplitBoundaryAtEnd(t *testing.T) {
	t.Parallel()

	static, dynamic, err := Split("static content" + DynamicBoundary)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if static != "static content" {
		t.Errorf("static = %q, want %q", static, "static content")
	}
	if dynamic != "" {
		t.Errorf("dynamic = %q, want empty", dynamic)
	}
}

func TestSplitPreservesWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		staticW  string
		dynamicW string
		wantErr  error
	}{
		{
			name:     "no extra whitespace",
			input:    "STATIC" + DynamicBoundary + "DYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
			wantErr:  nil,
		},
		{
			name:     "newline before boundary",
			input:    "STATIC\n" + DynamicBoundary + "DYNAMIC",
			staticW:  "STATIC\n",
			dynamicW: "DYNAMIC",
			wantErr:  nil,
		},
		{
			name:     "newline after boundary",
			input:    "STATIC" + DynamicBoundary + "\nDYNAMIC",
			staticW:  "STATIC",
			dynamicW: "\nDYNAMIC",
			wantErr:  nil,
		},
		{
			name:     "newline before and after",
			input:    "STATIC\n" + DynamicBoundary + "\nDYNAMIC",
			staticW:  "STATIC\n",
			dynamicW: "\nDYNAMIC",
			wantErr:  nil,
		},
		{
			name:     "multiple whitespace lines",
			input:    "STATIC\n  \n" + DynamicBoundary + "\n  \nDYNAMIC",
			staticW:  "STATIC\n  \n",
			dynamicW: "\n  \nDYNAMIC",
			wantErr:  nil,
		},
		{
			name:     "empty static",
			input:    DynamicBoundary + "DYNAMIC",
			staticW:  "",
			dynamicW: "DYNAMIC",
			wantErr:  nil,
		},
		{
			name:     "empty dynamic",
			input:    "STATIC" + DynamicBoundary,
			staticW:  "STATIC",
			dynamicW: "",
			wantErr:  nil,
		},
		{
			name:    "multiple boundaries",
			input:   "A" + DynamicBoundary + "MID" + DynamicBoundary + "B",
			wantErr: ErrMultipleBoundaries,
		},
		{
			name:    "three boundaries",
			input:   "A" + DynamicBoundary + "B" + DynamicBoundary + "C" + DynamicBoundary + "D",
			wantErr: ErrMultipleBoundaries,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			static, dynamic, err := Split(tt.input)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Split: unexpected error %v", err)
				}
				if static != tt.staticW {
					t.Errorf("static = %q, want %q", static, tt.staticW)
				}
				if dynamic != tt.dynamicW {
					t.Errorf("dynamic = %q, want %q", dynamic, tt.dynamicW)
				}
			} else {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Split: expected %v, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestSplitAndTrimRemovesWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		staticW  string
		dynamicW string
	}{
		{
			name:     "no extra whitespace",
			input:    "STATIC" + DynamicBoundary + "DYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "newline before boundary",
			input:    "STATIC\n" + DynamicBoundary + "DYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "newline after boundary",
			input:    "STATIC" + DynamicBoundary + "\nDYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "newline before and after",
			input:    "STATIC\n" + DynamicBoundary + "\nDYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "multiple whitespace lines",
			input:    "STATIC\n  \n" + DynamicBoundary + "\n  \nDYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "trailing static whitespace",
			input:    "STATIC\n\n  \n" + DynamicBoundary + "DYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "leading dynamic whitespace",
			input:    "STATIC" + DynamicBoundary + "\n\n  \nDYNAMIC",
			staticW:  "STATIC",
			dynamicW: "DYNAMIC",
		},
		{
			name:     "spaces within content preserved",
			input:    "  STATIC TEXT\n" + DynamicBoundary + "\nDYNAMIC TEXT  ",
			staticW:  "  STATIC TEXT",
			dynamicW: "DYNAMIC TEXT  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			static, dynamic, err := SplitAndTrim(tt.input)
			if err != nil {
				t.Fatalf("SplitAndTrim: unexpected error %v", err)
			}
			if static != tt.staticW {
				t.Errorf("static = %q, want %q", static, tt.staticW)
			}
			if dynamic != tt.dynamicW {
				t.Errorf("dynamic = %q, want %q", dynamic, tt.dynamicW)
			}
		})
	}
}

func TestSplitAndTrimErrors(t *testing.T) {
	t.Parallel()

	_, _, err := SplitAndTrim("no boundary")
	if !errors.Is(err, ErrBoundaryNotFound) {
		t.Errorf("expected ErrBoundaryNotFound, got %v", err)
	}

	_, _, err = SplitAndTrim("A" + DynamicBoundary + "B" + DynamicBoundary + "C")
	if !errors.Is(err, ErrMultipleBoundaries) {
		t.Errorf("expected ErrMultipleBoundaries, got %v", err)
	}
}

func TestInsertBoundaryRoundTrip(t *testing.T) {
	t.Parallel()

	static, dynamic := "hello\nworld", "dynamic content\nwith newline"
	combined := InsertBoundary(static, dynamic)

	splittedStatic, splittedDynamic, err := Split(combined)
	if err != nil {
		t.Fatalf("Split round trip: %v", err)
	}
	// InsertBoundary wraps with newlines, so Split sees them as part of static/dynamic
	if splittedStatic != static+"\n" {
		t.Errorf("static = %q, want %q", splittedStatic, static+"\n")
	}
	if splittedDynamic != "\n"+dynamic {
		t.Errorf("dynamic = %q, want %q", splittedDynamic, "\n"+dynamic)
	}

	// SplitAndTrim should restore exact original
	trimmedStatic, trimmedDynamic, err := SplitAndTrim(combined)
	if err != nil {
		t.Fatalf("SplitAndTrim round trip: %v", err)
	}
	if trimmedStatic != static {
		t.Errorf("trimmed static = %q, want %q", trimmedStatic, static)
	}
	if trimmedDynamic != dynamic {
		t.Errorf("trimmed dynamic = %q, want %q", trimmedDynamic, dynamic)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:    "no boundary",
			input:   "plain text",
			wantErr: ErrBoundaryNotFound,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: ErrBoundaryNotFound,
		},
		{
			name:  "single boundary",
			input: "static" + DynamicBoundary + "dynamic",
		},
		{
			name:  "boundary only",
			input: DynamicBoundary,
		},
		{
			name:    "two boundaries",
			input:   "A" + DynamicBoundary + "B" + DynamicBoundary + "C",
			wantErr: ErrMultipleBoundaries,
		},
		{
			name:    "three boundaries",
			input:   "A" + DynamicBoundary + "B" + DynamicBoundary + "C" + DynamicBoundary + "D",
			wantErr: ErrMultipleBoundaries,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Validate(tt.input)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate: unexpected error %v", err)
				}
			} else {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Validate: expected %v, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestSentinelErrorsUsable(t *testing.T) {
	t.Parallel()

	if !errors.Is(ErrBoundaryNotFound, ErrBoundaryNotFound) {
		t.Error("ErrBoundaryNotFound should be usable with errors.Is")
	}
	if !errors.Is(ErrMultipleBoundaries, ErrMultipleBoundaries) {
		t.Error("ErrMultipleBoundaries should be usable with errors.Is")
	}
	if errors.Is(ErrBoundaryNotFound, ErrMultipleBoundaries) {
		t.Error("sentinel errors should not be equal to each other")
	}
}
