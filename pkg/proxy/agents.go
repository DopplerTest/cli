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

	"gopkg.in/yaml.v3"
)

// AgentProfile is a named agent: the Doppler project and config it reads, the
// command the sandbox runs, the host directories mounted into it, and the service
// token minted for it. Its proxy config (bindings, passthrough, ...) lives beside it
// as config.yaml, and its proxy state (CA, agent env, log) under proxy/.
type AgentProfile struct {
	Name      string   `yaml:"name" json:"name"`
	Project   string   `yaml:"project" json:"project"`
	Config    string   `yaml:"config" json:"config"`
	Command   []string `yaml:"command" json:"command"`
	Mounts    []string `yaml:"mounts" json:"mounts"`
	TokenSlug string   `yaml:"token_slug,omitempty" json:"token_slug"`
	TokenName string   `yaml:"token_name,omitempty" json:"token_name"`
	// TokenExpiresAt is the RFC3339 expiry the API reports for the service token,
	// or "" when the token was minted without one.
	TokenExpiresAt string `yaml:"token_expires_at,omitempty" json:"token_expires_at"`
	CreatedAt      string `yaml:"created_at" json:"created_at"`
}

const (
	agentProfileFile = "agent.yaml"
	agentTokenFile   = "token"
	agentDataDirName = "proxy"
)

var agentNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateAgentName rejects names that would not be safe as a directory or a tag.
func ValidateAgentName(name string) error {
	if !agentNameRe.MatchString(name) {
		return fmt.Errorf("agent name %q must be lowercase letters, digits, - or _, starting with a letter or digit", name)
	}
	return nil
}

// AgentsDir is where every agent profile lives.
func AgentsDir(configDir string) string { return filepath.Join(configDir, "agents") }

// AgentDir is one agent's directory.
func AgentDir(configDir, name string) string { return filepath.Join(AgentsDir(configDir), name) }

// AgentConfigPath is the proxy config the agent runs with.
func AgentConfigPath(dir string) string { return filepath.Join(dir, proxyConfigFileName) }

// AgentDataDir is where the agent's proxy keeps its CA, agent env and log.
func AgentDataDir(dir string) string { return filepath.Join(dir, agentDataDirName) }

// AgentTokenPath is the file holding the agent's service token, mode 0600.
func AgentTokenPath(dir string) string { return filepath.Join(dir, agentTokenFile) }

const proxyConfigFileName = "config.yaml"

// LoadAgent reads one profile. A missing directory is os.ErrNotExist.
func LoadAgent(configDir, name string) (*AgentProfile, error) {
	if err := ValidateAgentName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(AgentDir(configDir, name), agentProfileFile))
	if err != nil {
		return nil, err
	}
	var p AgentProfile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("agent %s: %w", name, err)
	}
	if p.Name == "" {
		p.Name = name
	}
	return &p, nil
}

// SaveAgent writes the profile, creating its directory.
func SaveAgent(configDir string, p *AgentProfile) error {
	if err := ValidateAgentName(p.Name); err != nil {
		return err
	}
	dir := AgentDir(configDir, p.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, agentProfileFile), data, 0o600)
}

// ListAgents returns every profile, sorted by name. Directories without a readable
// profile are skipped.
func ListAgents(configDir string) ([]AgentProfile, error) {
	entries, err := os.ReadDir(AgentsDir(configDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []AgentProfile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := LoadAgent(configDir, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// WriteAgentToken stores the agent's service token beside its profile.
func WriteAgentToken(dir, token string) error {
	return os.WriteFile(AgentTokenPath(dir), []byte(token+"\n"), 0o600)
}

// ReadAgentToken returns the stored token, or "" when there is none.
func ReadAgentToken(dir string) string {
	data, err := os.ReadFile(AgentTokenPath(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// DefaultAgentPort is where the first agent's proxy listens, kept clear of the plain
// proxy start default (14322); each further agent
// takes the next port not already claimed by another agent's config.
const DefaultAgentPort = 14330

// NextAgentPort returns the lowest port from DefaultAgentPort upward that no
// existing agent's config.yaml listens on.
func NextAgentPort(configDir string) int {
	agents, _ := ListAgents(configDir)
	taken := map[int]bool{}
	for _, a := range agents {
		addr := ReadListenAddress(AgentConfigPath(AgentDir(configDir, a.Name)))
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			var port int
			if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err == nil {
				taken[port] = true
			}
		}
	}
	port := DefaultAgentPort
	for taken[port] {
		port++
	}
	return port
}

// AgentPort returns the port an agent's proxy listens on, from its config.yaml.
func AgentPort(configDir, name string) int {
	addr := ReadListenAddress(AgentConfigPath(AgentDir(configDir, name)))
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		var port int
		if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err == nil {
			return port
		}
	}
	return DefaultAgentPort
}

// AgentHomeVolume names the Docker volume that keeps the agent's home directory
// between runs, so a login done once inside the sandbox sticks.
func AgentHomeVolume(name string) string { return "doppler-agent-" + name + "-home" }

// ParseMount splits a mount spec of the form /host/path[:ro] and resolves the
// host path to an absolute directory.
func ParseMount(spec string) (hostPath string, readOnly bool, err error) {
	spec = strings.TrimSpace(spec)
	if strings.HasSuffix(spec, ":ro") {
		readOnly = true
		spec = strings.TrimSuffix(spec, ":ro")
	} else {
		spec = strings.TrimSuffix(spec, ":rw")
	}
	if spec == "" {
		return "", false, errors.New("empty mount path")
	}
	abs, err := filepath.Abs(spec)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("%s is not a directory", abs)
	}
	return abs, readOnly, nil
}
