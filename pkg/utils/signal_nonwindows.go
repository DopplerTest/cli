//go:build !windows
// +build !windows

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
package utils

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// highestSignal is the highest signal number of any system, the 64 of Linux.
const highestSignal = 64

// jobSignals returns every signal but the three that stop a job. A process that catches
// those keeps running while the rest of its job is stopped, and the shell waits for it
// instead of taking the terminal back. os/signal cannot give a signal its stop back
// once it has caught every signal, so these three are left out from the start.
func jobSignals() []os.Signal {
	signals := make([]os.Signal, 0, highestSignal)
	for number := syscall.Signal(1); number <= highestSignal; number++ {
		if number != syscall.SIGTSTP && number != syscall.SIGTTIN && number != syscall.SIGTTOU {
			signals = append(signals, number)
		}
	}
	return signals
}

// inForegroundOfTerminal reports whether this process is in the foreground job of its
// controlling terminal, the one the terminal sends the signals of its keys to. The
// streams of the process do not show that: with stdin from a pipe or stdout sent to a
// file it is still in that job.
func inForegroundOfTerminal() bool {
	terminal, err := syscall.Open("/dev/tty", syscall.O_RDONLY|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer syscall.Close(terminal) // #nosec G104
	foreground, err := unix.IoctlGetInt(terminal, unix.TIOCGPGRP)
	return err == nil && foreground == syscall.Getpgrp()
}
