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
//
// A shared environment of the same format lives at the top of the config directory.
// Every agent inherits its entries, and an agent's own entry wins when both hold the
// same key, so a credential can be stored once for the user rather than per agent.
const agentLocalEnvFile = "env"

var envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// AgentLocalEnvPath is the file holding the agent's local environment, mode 0600.
func AgentLocalEnvPath(dir string) string { return filepath.Join(dir, agentLocalEnvFile) }

// SharedEnvPath is the file holding the environment every agent inherits, mode 0600.
func SharedEnvPath(configDir string) string { return filepath.Join(configDir, agentLocalEnvFile) }

// ValidateEnvKey rejects anything that is not a conventional environment variable name.
func ValidateEnvKey(key string) error {
	if !envKeyRe.MatchString(key) {
		return fmt.Errorf("%q is not a valid environment variable name (use uppercase letters, digits and _)", key)
	}
	return nil
}

// ReadAgentEnv returns the agent's stored entries. A missing file is an empty environment.
func ReadAgentEnv(dir string) (map[string]string, error) {
	return readEnvFile(AgentLocalEnvPath(dir))
}

// SetAgentEnv stores one entry for the agent, replacing any existing value for the key.
func SetAgentEnv(dir, key, value string) error {
	return setEnvFile(AgentLocalEnvPath(dir), key, value)
}

// UnsetAgentEnv removes one of the agent's entries. A missing key is not an error.
func UnsetAgentEnv(dir, key string) error {
	return unsetEnvFile(AgentLocalEnvPath(dir), key)
}

// AgentEnvKeys returns the agent's stored names, sorted. Values are never listed.
func AgentEnvKeys(dir string) []string {
	return envFileKeys(AgentLocalEnvPath(dir))
}

// ReadSharedEnv returns the shared entries. A missing file is an empty environment.
func ReadSharedEnv(configDir string) (map[string]string, error) {
	return readEnvFile(SharedEnvPath(configDir))
}

// SetSharedEnv stores one shared entry, replacing any existing value for the key.
func SetSharedEnv(configDir, key, value string) error {
	return setEnvFile(SharedEnvPath(configDir), key, value)
}

// UnsetSharedEnv removes one shared entry. A missing key is not an error.
func UnsetSharedEnv(configDir, key string) error {
	return unsetEnvFile(SharedEnvPath(configDir), key)
}

// SharedEnvKeys returns the shared names, sorted. Values are never listed.
func SharedEnvKeys(configDir string) []string {
	return envFileKeys(SharedEnvPath(configDir))
}

// MergeAgentEnv lays the agent's own entries over the shared ones, so the agent's
// value wins when both hold the same key. The result is what the sandbox receives.
func MergeAgentEnv(agent, shared map[string]string) map[string]string {
	merged := make(map[string]string, len(agent)+len(shared))
	for key, value := range shared {
		merged[key] = value
	}
	for key, value := range agent {
		merged[key] = value
	}
	return merged
}

func readEnvFile(path string) (map[string]string, error) {
	env := map[string]string{}
	data, err := os.ReadFile(path)
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

func setEnvFile(path, key, value string) error {
	if err := ValidateEnvKey(key); err != nil {
		return err
	}
	if value == "" {
		return errors.New("the value is empty")
	}
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("the value must be a single line")
	}
	env, err := readEnvFile(path)
	if err != nil {
		return err
	}
	env[key] = value
	return writeEnvFile(path, env)
}

func unsetEnvFile(path, key string) error {
	env, err := readEnvFile(path)
	if err != nil {
		return err
	}
	if _, ok := env[key]; !ok {
		return nil
	}
	delete(env, key)
	return writeEnvFile(path, env)
}

func envFileKeys(path string) []string {
	env, err := readEnvFile(path)
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

func writeEnvFile(path string, env map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, key := range sortedEnvKeys(env) {
		b.WriteString(key + "=" + env[key] + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
