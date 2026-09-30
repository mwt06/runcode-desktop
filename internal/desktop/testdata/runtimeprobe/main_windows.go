// A console-subsystem fixture for the startup runtime version probe.
package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--version" {
		os.Exit(2)
	}
	hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		// No version in the diagnostic: the production parser must not mistake
		// this failure for a successful probe.
		fmt.Fprint(os.Stderr, "unexpected console")
		os.Exit(90)
	}
	switch os.Getenv("RUNCODE_RUNTIME_PROBE_FIXTURE") {
	case "stdout":
		fmt.Fprintln(os.Stdout, "Python 3.12.11")
	case "stderr":
		fmt.Fprintln(os.Stderr, "Python 2.7.18")
	case "nonzero-version":
		fmt.Fprintln(os.Stderr, "git version 2.50.0.windows.1")
		os.Exit(1)
	case "fail":
		os.Exit(2)
	case "wait":
		time.Sleep(time.Minute)
	default:
		os.Exit(2)
	}
}
