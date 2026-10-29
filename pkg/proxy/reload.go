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
	"bytes"
	"context"
	"os"
	"time"

	agentproxy "github.com/DopplerTest/agent-proxy"
)

// Reloader is an engine that can take a change of policy while it runs. Restarting
// the proxy instead would mint new masks and strand the ones a running agent holds.
type Reloader interface {
	Reload(agentproxy.Policy)
}

// PolicyFor is the part of cfg a running proxy can take without a restart, with the
// --passthrough hosts added to the config's own.
func PolicyFor(cfg *ProxyConfig, extraPassthrough []string) (agentproxy.Policy, error) {
	binding, err := cfg.BindingResolver()
	if err != nil {
		return agentproxy.Policy{}, err
	}
	allowUpgrades, err := cfg.AllowProtocolUpgrades()
	if err != nil {
		return agentproxy.Policy{}, err
	}
	return agentproxy.Policy{
		Binding:               binding,
		Methods:               cfg.MethodConfigs(),
		PassthroughHosts:      MergePassthrough(cfg, extraPassthrough),
		AllowProtocolUpgrades: allowUpgrades,
	}, nil
}

// ConfigWatcher notices when the proxy config file changes, so a running proxy can
// take what the user saved without a restart.
type ConfigWatcher struct {
	path string
	last []byte // the contents last reported, as a config or as an error
}

// NewConfigWatcher watches path from its contents now, so only a later change is
// reported.
func NewConfigWatcher(path string) *ConfigWatcher {
	data, _ := os.ReadFile(path)
	return &ConfigWatcher{path: path, last: data}
}

// Check returns the config when the file's contents differ from the last ones it
// reported, or the parse error when the new contents are not a valid config. Each
// version of the file is reported once. A file that cannot be read or is blank is
// not reported: an editor that replaces the file leaves it briefly missing or empty,
// and the complete file follows.
func (w *ConfigWatcher) Check() (*ProxyConfig, error) {
	data, err := os.ReadFile(w.path)
	if err != nil || bytes.Equal(data, w.last) || len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	w.last = data
	return parseProxyConfig(data)
}

// Watch hands each change to the config to apply, or the reason the changed file was
// not taken to fail, until ctx ends, checking every interval.
func (w *ConfigWatcher) Watch(ctx context.Context, interval time.Duration, apply func(*ProxyConfig), fail func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg, err := w.Check()
			if err != nil {
				fail(err)
			} else if cfg != nil {
				apply(cfg)
			}
		}
	}
}
