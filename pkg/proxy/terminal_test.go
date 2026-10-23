//go:build !windows

/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxy

import (
	"os"
	"os/exec"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// The wrapper needs a terminal on stdin to mirror. `go test` rarely has one, so the test
// hands it a pseudo-terminal of its own.
func TestRunUnderTerminalCopiesWhatTheChildWrites(t *testing.T) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		ptmx, tty, err := pty.Open()
		if err != nil {
			t.Skipf("no pseudo-terminal available: %v", err)
		}
		defer ptmx.Close()
		defer tty.Close()
		saved := os.Stdin
		os.Stdin = tty
		defer func() { os.Stdin = saved }()
	}
	var copied []string
	cmd := exec.Command("sh", "-c", `printf 'ready\033]52;c;aGVsbG8gd29ybGQ=\a\n'; exit 3`)
	code, err := RunUnderTerminal(cmd, func(text string) { copied = append(copied, text) })
	if err != nil {
		t.Fatalf("RunUnderTerminal: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if len(copied) != 1 || copied[0] != "hello world" {
		t.Fatalf("copied = %q, want [hello world]", copied)
	}
}
