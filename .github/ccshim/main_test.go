package main

import (
	"reflect"
	"testing"
)

func TestClangArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"compile", []string{"-mthreads", "-c", "a.c", "-o", "a.o"}, []string{"-c", "a.c", "-o", "a.o"}},
		{"gui", []string{"-mthreads", "-mwindows", "main.o", "-o", "app.exe"}, []string{"-Xlinker", "/subsystem:windows", "-Xlinker", "/entry:mainCRTStartup", "main.o", "-o", "app.exe"}},
		{"explicit entry", []string{"-mwindows", "-Wl,/entry:custom", "main.o"}, []string{"-Xlinker", "/subsystem:windows", "-Wl,/entry:custom", "main.o"}},
		{"explicit entry before GUI", []string{"-Xlinker", "/ENTRY:custom", "-mwindows"}, []string{"-Xlinker", "/ENTRY:custom", "-Xlinker", "/subsystem:windows"}},
		{"console", []string{"-mconsole", "main.o"}, []string{"-Xlinker", "/subsystem:console", "main.o"}},
		{"unchanged", []string{"-shared", "a.o", "-Wl,-entry:main"}, []string{"-shared", "a.o", "-Wl,-entry:main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clangArgs(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args=%q, want %q", got, tc.want)
			}
		})
	}
}
