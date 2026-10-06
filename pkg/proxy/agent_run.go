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
	"time"
)

// The agent's last run: a small JSON record beside the profile that `agent run` writes
// when the sandbox starts and completes when it exits. The proxy outlives any one run
// and the run itself happens in whatever terminal launched it, so this record is how
// the desktop app (or `agent profile --json`) learns where one run ended and the next
// began. It holds no secrets.
const agentLastRunFile = "last-run.json"

type LastRun struct {
	StartedAt time.Time `json:"started_at"`
	// EndedAt and ExitCode are nil while the sandbox is still running.
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

func WriteLastRun(dir string, run LastRun) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	return os.WriteFile(AgentLastRunPath(dir), data, 0o644)
}

// ExitCodeOf maps the error from running the sandbox to the exit code to record:
// the process's own code when there was one, 0 for success, and 1 for a failure
// that never produced a process (docker missing, bad mounts).
func ExitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return 1
}

// IsStopExitCode reports whether an exit code means the sandbox was told to stop
// (SIGTERM from `agent stop` or docker, Ctrl-C from the user) rather than failed.
func IsStopExitCode(code int) bool { return code == 143 || code == 130 }
