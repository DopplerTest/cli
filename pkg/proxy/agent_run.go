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
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// The agent's last run: a small JSON record beside the profile that `agent run` writes
// when the command starts and completes when it exits. The proxy outlives any one run
// and the run itself happens in whatever terminal launched it, so this record is how
// the desktop app (or `agent profile --json`) learns where one run ended and the next
// began. It holds no secrets.
const agentLastRunFile = "last-run.json"

type LastRun struct {
	StartedAt time.Time `json:"started_at"`
	// EndedAt and ExitCode are nil while the command is still running.
	EndedAt  *time.Time `json:"ended_at"`
	ExitCode *int       `json:"exit_code"`
	Command  []string   `json:"command"`
}

func (r LastRun) Finished() bool { return r.EndedAt != nil }

func AgentLastRunPath(dir string) string { return filepath.Join(dir, agentLastRunFile) }

// ReadLastRun returns nil, nil for an agent that has never run.
func ReadLastRun(dir string) (*LastRun, error) {
	data, err := os.ReadFile(AgentLastRunPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run LastRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// WriteLastRun writes the record into the agent's directory, and writes nothing for an
// agent with no directory. That is an agent deleted while its command was running. A
// directory made for the record would bring the agent's name back, and an agent created
// with that name later would start with the last run of the deleted one.
func WriteLastRun(dir string, run LastRun) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	err = os.WriteFile(AgentLastRunPath(dir), data, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ExitCodeOf maps the error from running the command to the exit code to record:
// the process's own code when there was one, 0 for success, and 1 for a failure
// that never produced a process (a command that is not installed).
func ExitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// A process ended by a signal has no code of its own. Record what a shell
		// reports for it, so a stop still reads as 130 or 143.
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exit.ExitCode()
	}
	return 1
}

// IsStopExitCode reports whether an exit code means the run was told to stop
// (SIGTERM, or Ctrl-C from the user) rather than failed.
func IsStopExitCode(code int) bool { return code == 143 || code == 130 }
