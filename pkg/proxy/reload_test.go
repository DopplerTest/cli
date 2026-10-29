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
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	agentproxy "github.com/DopplerTest/agent-proxy"
)

func writeConfig(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The watcher reports each saved version of the config once: nothing for the file it
// started with or for a save that changes nothing, the config for a valid change, and
// the reason for an invalid one, which is not repeated until the file changes again.
func TestConfigWatcherReportsEachChangeOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "unbound: deny\n")
	w := NewConfigWatcher(path)

	if cfg, err := w.Check(); cfg != nil || err != nil {
		t.Fatalf("the starting file is not a change; got %+v, %v", cfg, err)
	}

	writeConfig(t, path, "bindings:\n  GH:\n    - host: api.github.com\n")
	cfg, err := w.Check()
	if err != nil || cfg == nil || cfg.Bindings["GH"][0].Host != "api.github.com" {
		t.Fatalf("a valid change should be reported; got %+v, %v", cfg, err)
	}
	if cfg, err := w.Check(); cfg != nil || err != nil {
		t.Fatalf("an unchanged file is reported once; got %+v, %v", cfg, err)
	}

	writeConfig(t, path, "bindings: [unclosed\n")
	if _, err := w.Check(); err == nil {
		t.Fatal("a change that does not parse should report why")
	}
	if cfg, err := w.Check(); cfg != nil || err != nil {
		t.Fatalf("the same broken file is reported once; got %+v, %v", cfg, err)
	}

	// A save in progress leaves the file blank for a moment; the save that follows is
	// what counts.
	writeConfig(t, path, "")
	if cfg, err := w.Check(); cfg != nil || err != nil {
		t.Fatalf("a blank file is not a change; got %+v, %v", cfg, err)
	}
	writeConfig(t, path, "unbound: trust-first-use\n")
	if cfg, err := w.Check(); err != nil || cfg == nil || cfg.Unbound != "trust-first-use" {
		t.Fatalf("the completed save should be reported; got %+v, %v", cfg, err)
	}
}

func TestConfigWatcherWatchAppliesASave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "unbound: deny\n")
	w := NewConfigWatcher(path)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	applied := make(chan *ProxyConfig, 1)
	go w.Watch(ctx, 10*time.Millisecond, func(cfg *ProxyConfig) { applied <- cfg }, func(err error) { t.Errorf("unexpected: %v", err) })

	writeConfig(t, path, "unbound: trust-first-use\n")
	select {
	case cfg := <-applied:
		if cfg.Unbound != "trust-first-use" {
			t.Fatalf("applied the wrong config: %+v", cfg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the save was never applied")
	}
}

// PolicyFor carries every setting a running proxy can change: bindings and the
// unbound policy, credential methods, passthrough hosts with the flag's added, and
// the upgrade policy.
func TestPolicyForCarriesTheReloadableSettings(t *testing.T) {
	cfg := &ProxyConfig{
		Passthrough:      []string{"api.anthropic.com"},
		Bindings:         map[string][]agentproxy.Rule{"GH": {{Host: "api.github.com"}}},
		Methods:          map[string]CredentialMethod{"AWS_SECRET_ACCESS_KEY": {Kind: "aws_sigv4", Region: "us-west-2", AccessKeyID: "AWS_ACCESS_KEY_ID"}},
		ProtocolUpgrades: "tunnel",
	}
	p, err := PolicyFor(cfg, []string{"claude.ai"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.PassthroughHosts, []string{"api.anthropic.com", "claude.ai"}) {
		t.Fatalf("passthrough should be the config's plus the flag's; got %v", p.PassthroughHosts)
	}
	if ok, _ := p.Binding.Allowed(agentproxy.BindingRequest{Name: "GH", Dest: agentproxy.Destination{Host: "api.github.com"}}); !ok {
		t.Fatal("the binding should allow its declared host")
	}
	if m := p.Methods["AWS_SECRET_ACCESS_KEY"]; m.Kind != "aws_sigv4" || m.Region != "us-west-2" || m.AccessKeyID != "AWS_ACCESS_KEY_ID" {
		t.Fatalf("the method did not carry over: %+v", m)
	}
	if !p.AllowProtocolUpgrades {
		t.Fatal("protocol_upgrades: tunnel should allow upgrades")
	}

	if _, err := PolicyFor(&ProxyConfig{Unbound: "maybe"}, nil); err == nil {
		t.Fatal("an unknown unbound policy must be an error")
	}
}
