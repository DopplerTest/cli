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
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// RunUnderTerminal runs cmd on a pseudo-terminal that stands in for the one on this
// process's stdin: the child sees a real TTY, which `docker run -it` insists on, while every
// byte it prints passes through here on its way to the user's terminal. Clipboard writes the
// child makes with OSC 52 go to onCopy. The window size follows the real terminal, and
// SIGINT, SIGTERM and SIGHUP are passed on to the child. It returns the child's exit code.
func RunUnderTerminal(cmd *exec.Cmd, onCopy func(text string)) (int, error) {
	stdin := int(os.Stdin.Fd())
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return 1, err
	}
	// The real terminal goes raw so keystrokes reach the child unedited; the child's own
	// terminal handling (docker's, then the agent's) takes it from there.
	state, err := term.MakeRaw(stdin)
	if err != nil {
		return 1, err
	}
	defer term.Restore(stdin, state) //nolint:errcheck

	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return 1, err
	}
	defer ptmx.Close()

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if sig == syscall.SIGWINCH {
				if size, err := pty.GetsizeFull(os.Stdin); err == nil {
					pty.Setsize(ptmx, size) //nolint:errcheck
				}
				continue
			}
			if cmd.Process != nil {
				cmd.Process.Signal(sig) //nolint:errcheck
			}
		}
	}()

	go io.Copy(ptmx, os.Stdin) //nolint:errcheck
	// The copy ends with an EIO once the child closes its side of the terminal, which is
	// the normal end of a session rather than a failure.
	outputErr := ForwardOSC52(os.Stdout, ptmx, onCopy)

	waitErr := cmd.Wait()
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, waitErr
	}
	if outputErr != nil && !errors.Is(outputErr, syscall.EIO) {
		return 0, outputErr
	}
	return 0, nil
}
