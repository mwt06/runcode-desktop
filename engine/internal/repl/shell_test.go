package repl

import "os"

// The Bash tool defaults to cmd on Windows; these tests run bash commands
// (printf, etc.) and assert bash semantics, so force bash for consistency across
// platforms.
func init() { os.Setenv("RUNCODE_SHELL", "bash") }
