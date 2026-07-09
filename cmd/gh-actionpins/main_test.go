package main

import "testing"

func TestRunHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		if code := run(args); code != 0 {
			t.Errorf("run(%v) = %d, want 0", args, code)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()
	if code := run([]string{"not-a-command"}); code != 1 {
		t.Errorf("run([not-a-command]) = %d, want 1", code)
	}
}
