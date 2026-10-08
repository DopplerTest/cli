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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The agent's local environment: values such as its provider credential that are
// injected into the sandbox by value. It lives beside the profile as a 0600 file of
// KEY=value lines and is never written to Doppler or handled by the proxy. `agent run`
// exports each entry into its own process and names it to docker (`-e KEY`), so the
// value is inherited rather than written into the command line.
const agentLocalEnvFile = "env"

var envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// AgentLocalEnvPath is the file holding the agent's local environment, mode 0600.
func AgentLocalEnvPath(dir string) string { return filepath.Join(dir, agentLocalEnvFile) }

// ValidateEnvKey rejects anything that is not a conventional environment variable name.
func ValidateEnvKey(key string) error {
	if !envKeyRe.MatchString(key) {
		return fmt.Errorf("%q is not a valid environment variable name (use uppercase letters, digits and _)", key)
	}
	return nil
}

// ReadAgentEnv returns the stored entries. A missing file is an empty environment.
func ReadAgentEnv(dir string) (map[string]string, error) {
	env := map[string]string{}
	data, err := os.ReadFile(AgentLocalEnvPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return env, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		// Only the first '=' separates the key, so values may contain '='.
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	return env, nil
}

// SetAgentEnv stores one entry, replacing any existing value for the key.
func SetAgentEnv(dir, key, value string) error {
	if err := ValidateEnvKey(key); err != nil {
		return err
	}
	if value == "" {
		return errors.New("the value is empty")
	}
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("the value must be a single line")
	}
	env, err := ReadAgentEnv(dir)
	if err != nil {
		return err
	}
	env[key] = value
	return writeAgentEnv(dir, env)
}

// UnsetAgentEnv removes one entry. A missing key is not an error.
func UnsetAgentEnv(dir, key string) error {
	env, err := ReadAgentEnv(dir)
	if err != nil {
		return err
	}
	if _, ok := env[key]; !ok {
		return nil
	}
	delete(env, key)
	return writeAgentEnv(dir, env)
}

// AgentEnvKeys returns the stored names, sorted. Values are never listed.
func AgentEnvKeys(dir string) []string {
	env, err := ReadAgentEnv(dir)
	if err != nil {
		return nil
	}
	return sortedEnvKeys(env)
}

// ExportAgentEnv puts every entry into this process's environment and returns the
// sorted names. The sandbox passes those to docker as `-e NAME`, so the values are
// inherited from this process and never appear in docker's command line.
func ExportAgentEnv(env map[string]string) []string {
	for key, value := range env {
		os.Setenv(key, value)
	}
	return sortedEnvKeys(env)
}

func writeAgentEnv(dir string, env map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, key := range sortedEnvKeys(env) {
		b.WriteString(key + "=" + env[key] + "\n")
	}
	return os.WriteFile(AgentLocalEnvPath(dir), []byte(b.String()), 0o600)
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
