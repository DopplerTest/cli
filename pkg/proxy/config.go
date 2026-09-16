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
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	agentproxy "github.com/DopplerTest/agent-proxy"
	"gopkg.in/yaml.v3"
)

// ProxyConfig is the user-editable proxy configuration (config.yaml).
type ProxyConfig struct {
	// ListenAddress is the address the proxy binds. Defaults (via the starter
	// config) to 0.0.0.0:14322 so the `doppler agent run` sandbox can reach it.
	// The --address flag overrides this.
	ListenAddress string `yaml:"listen_address"`

	// Passthrough lists hostnames the proxy blind-tunnels instead of
	// intercepting (no TLS termination, no injection).
	Passthrough []string `yaml:"passthrough"`

	// Bindings declares where each secret may be injected, by secret name. A
	// secret with no entry falls under Unbound.
	Bindings map[string][]agentproxy.Rule `yaml:"bindings"`

	// Unbound is the policy for a secret with no bindings entry: "deny" (the
	// default) refuses it everywhere, "trust-first-use" pins it to the first
	// host the agent sends it to.
	Unbound string `yaml:"unbound"`

	// Methods declares a non-static credential method per secret name. A secret with
	// no entry uses the static method: its masked value is swapped in a header.
	Methods map[string]CredentialMethod `yaml:"methods"`

	// PassByValue names secrets the agent holds for real rather than as a mask,
	// typically its own model provider token whose host is passed through.
	PassByValue []string `yaml:"pass_by_value"`

	// MissingBindings is the startup check for secrets with no binding: "ignore",
	// "warn" (the default) or "fail". Unbound decides what happens to a request;
	// this decides what happens when the proxy starts.
	MissingBindings string `yaml:"missing_bindings"`

	// BindingSync keeps the bindings section in step with the Doppler config on each
	// start, adding a commented stub for a new secret and removing a commented stub
	// for one that is gone. Nil means true.
	BindingSync *bool `yaml:"binding_sync"`

	// ConfigOverrides replaces parts of this config per Doppler config name, so one
	// file can serve dev and prd with different destinations.
	ConfigOverrides map[string]ConfigOverride `yaml:"config_overrides"`

	// ProtocolUpgrades is the policy for WebSocket and other protocol upgrades:
	// "refuse" (the default) rejects the handshake, "tunnel" brokers it and then
	// stops inspecting once frames start.
	ProtocolUpgrades string `yaml:"protocol_upgrades"`
}

// AllowProtocolUpgrades maps the policy to the engine flag.
func (c *ProxyConfig) AllowProtocolUpgrades() (bool, error) {
	switch c.ProtocolUpgrades {
	case "", "refuse":
		return false, nil
	case "tunnel":
		return true, nil
	default:
		return false, fmt.Errorf("protocol_upgrades must be refuse or tunnel, got %q", c.ProtocolUpgrades)
	}
}

// ConfigOverride replaces parts of the base config for one Doppler config. A key the
// override omits is inherited. A key it sets REPLACES the base outright rather than
// merging, so the effective value for any key comes from exactly one place in the file:
// an override naming a secret replaces that secret's whole rule list. An explicitly
// secret named under "bindings" has its rule list replaced outright; a secret the
// override leaves out keeps its base binding, and "SECRET: []" binds that one nowhere.
type ConfigOverride struct {
	Passthrough      []string                     `yaml:"passthrough"`
	Bindings         map[string][]agentproxy.Rule `yaml:"bindings"`
	Unbound          string                       `yaml:"unbound"`
	Methods          map[string]CredentialMethod  `yaml:"methods"`
	PassByValue      []string                     `yaml:"pass_by_value"`
	ProtocolUpgrades string                       `yaml:"protocol_upgrades"`
	MissingBindings  string                       `yaml:"missing_bindings"`
	BindingSync      *bool                        `yaml:"binding_sync"`
}

// ResolveForConfig returns the effective config for the named Doppler config, and
// whether an override applied. The base is returned unchanged when none matches.
func (c *ProxyConfig) ResolveForConfig(name string) (*ProxyConfig, bool) {
	ov, ok := c.ConfigOverrides[name]
	if name == "" || !ok {
		return c, false
	}
	out := *c
	out.ConfigOverrides = nil
	if ov.Passthrough != nil {
		out.Passthrough = ov.Passthrough
	}
	// Per secret, not per map: a secret the override names has its rule list replaced,
	// and a secret it leaves out keeps the base binding. An empty list ("SECRET: []")
	// binds that secret nowhere.
	if ov.Bindings != nil {
		merged := make(map[string][]agentproxy.Rule, len(out.Bindings)+len(ov.Bindings))
		for k, v := range out.Bindings {
			merged[k] = v
		}
		for k, v := range ov.Bindings {
			merged[k] = v
		}
		out.Bindings = merged
	}
	if ov.Unbound != "" {
		out.Unbound = ov.Unbound
	}
	if ov.Methods != nil {
		merged := make(map[string]CredentialMethod, len(out.Methods)+len(ov.Methods))
		for k, v := range out.Methods {
			merged[k] = v
		}
		for k, v := range ov.Methods {
			merged[k] = v
		}
		out.Methods = merged
	}
	if ov.PassByValue != nil {
		out.PassByValue = ov.PassByValue
	}
	if ov.ProtocolUpgrades != "" {
		out.ProtocolUpgrades = ov.ProtocolUpgrades
	}
	if ov.MissingBindings != "" {
		out.MissingBindings = ov.MissingBindings
	}
	if ov.BindingSync != nil {
		out.BindingSync = ov.BindingSync
	}
	return &out, true
}

// CredentialMethod is how a secret is brokered onto a request (config.yaml).
// It maps to agentproxy.MethodConfig.
type CredentialMethod struct {
	// Kind: "static" (default), "oauth2_client_credentials", or "aws_sigv4".
	Kind string `yaml:"kind"`

	// OAuth2 client-credentials (kind: oauth2_client_credentials). The secret's value
	// is the client secret; the proxy exchanges it for a bearer and injects that.
	TokenURL string   `yaml:"token_url"`
	ClientID string   `yaml:"client_id"`
	Scopes   []string `yaml:"scopes"`

	// AWS SigV4 (kind: aws_sigv4). The secret is the AWS secret access key; access_key_id
	// names the secret holding the access key id. Region defaults to us-east-1.
	Service     string `yaml:"service"`
	Region      string `yaml:"region"`
	AccessKeyID string `yaml:"access_key_id"`
}

// BindingResolver builds the resolver the proxy authorizes injection with.
func (c *ProxyConfig) BindingResolver() (agentproxy.BindingResolver, error) {
	var policy agentproxy.UnboundPolicy
	switch c.Unbound {
	case "", "deny":
		policy = agentproxy.UnboundDeny
	case "trust-first-use":
		policy = agentproxy.UnboundTOFU
	default:
		return nil, fmt.Errorf("unbound must be deny or trust-first-use, got %q", c.Unbound)
	}
	return agentproxy.NewRuleResolver(c.Bindings, policy), nil
}

// MethodConfigs maps the user's credential-method declarations to the agent-proxy
// method registry. Returns nil when none are declared (every secret is static).
func (c *ProxyConfig) MethodConfigs() map[string]agentproxy.MethodConfig {
	if len(c.Methods) == 0 {
		return nil
	}
	out := make(map[string]agentproxy.MethodConfig, len(c.Methods))
	for name, m := range c.Methods {
		out[name] = agentproxy.MethodConfig{
			Kind:        m.Kind,
			TokenURL:    m.TokenURL,
			ClientID:    m.ClientID,
			Scopes:      m.Scopes,
			Service:     m.Service,
			Region:      m.Region,
			AccessKeyID: m.AccessKeyID,
		}
	}
	return out
}

// starterConfig is the config written on first run. Every binding is commented, so a
// scaffolded proxy injects nothing until the operator names a destination. The secret
// names are deliberately left out here: `proxy bindings` fills them in on demand, so
// the list stays current instead of freezing at whatever existed on the first run.
const starterConfig = `# Doppler agent credential proxy.

# 0.0.0.0 also serves the ` + "`agent run`" + ` sandbox. 127.0.0.1 is loopback only.
listen_address: 0.0.0.0:14322

# Hosts tunnelled without interception.
passthrough:
  - api.anthropic.com
  - console.anthropic.com
  - claude.ai
  - claude.com

# The hosts each secret may be sent to.
#
#   EXAMPLE_TOKEN:
#     - host: api.example.org
#       paths: ["/v1/**"]
#       methods: [GET, POST]
#     - host: api.example.com
bindings:

# Destination policy for a secret with no binding above.
#   deny              refuse the request at every host
#   trust-first-use   allow the first host it reaches, then only that host
unbound: deny

# Startup check for secrets with no binding above.
#   ignore   start anyway
#   warn     name them, then start
#   fail     refuse to start
missing_bindings: warn

# Keep the bindings above in sync with your Doppler config. When true, each start adds
# a commented line for every secret that is missing here, and removes commented lines
# for secrets that no longer exist in Doppler.
binding_sync: true

# Policy for WebSocket and other protocol upgrades.
#   refuse   reject the handshake
#   tunnel   broker the handshake, then copy frames in both directions verbatim
protocol_upgrades: refuse

# Credentials the proxy exchanges or signs rather than injecting the stored value.
# methods:
#   EXAMPLE_TOKEN:
#     kind: oauth2_client_credentials
#     token_url: https://api.example.com/oauth/token
#     client_id: <client id>
#     scopes: [read]

# Secrets handed to the agent as real values. Their host belongs in passthrough.
# pass_by_value:
#   - EXAMPLE_TOKEN

# Settings that apply to one Doppler config. A key here replaces the value above.
# config_overrides:
#   prd:
#     bindings:
#       EXAMPLE_TOKEN:
#         - host: api.example.com
#           paths: ["/v1/**"]
#     missing_bindings: fail
`

// LoadOrScaffold loads the proxy config from path. If the file does not exist (or is
// blank) it writes the starter config and returns it with created=true.
func LoadOrScaffold(path string) (cfg *ProxyConfig, created bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	// Write the starter config when the file is missing OR empty — so a stray blank
	// file (e.g. from an interrupted write) still gets populated on startup instead
	// of silently loading as an empty config.
	if errors.Is(err, os.ErrNotExist) || len(bytes.TrimSpace(data)) == 0 {
		if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
			return nil, false, err
		}
		cfg, err = parseProxyConfig([]byte(starterConfig))
		return cfg, true, err
	}
	cfg, err = parseProxyConfig(data)
	return cfg, false, err
}

func parseProxyConfig(data []byte) (*ProxyConfig, error) {
	var cfg ProxyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// MergePassthrough returns the config's passthrough hosts plus any extras,
// de-duplicated and order-preserving (config entries first).
func MergePassthrough(cfg *ProxyConfig, extra []string) []string {
	return mergeHostLists(cfg.Passthrough, extra)
}

func mergeHostLists(base, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, base...), extra...) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// bindingsHeadingRe matches the "bindings:" line that opens the section, commented or not.
var bindingsHeadingRe = regexp.MustCompile(`^#?\s*bindings:\s*$`)

// bindingStubNameRe matches an indented secret-name key inside the bindings section,
// commented or not: "  A_TOKEN:" and "  # A_TOKEN:" both yield A_TOKEN.
var bindingStubNameRe = regexp.MustCompile(`^(?:\s+#\s*|\s+)([A-Za-z_][A-Za-z0-9_]*):\s*$`)

// bindingBlockLineRe matches an indented line belonging to the bindings block, commented
// or not. A comment introducing the NEXT key sits at one space after the marker, so the
// the leading indent is what separates "  # A_TOKEN:" from a column-0 prose comment,
// which belongs to the key that follows the section.
var bindingBlockLineRe = regexp.MustCompile(`^(?:\s+#|\s{2,})\s*\S`)

// isTopLevelKey reports whether the line opens a new top-level config key, which is what
// ends the bindings section. Top-level keys sit at column 0 (after an optional comment
// marker and a single space); binding entries are indented further, so indentation is
// what separates "# unbound: deny" from "  # A_TOKEN:".
func isTopLevelKey(line string) bool {
	t := strings.TrimPrefix(line, "#")
	if len(t) > 0 && t[0] == ' ' {
		t = t[1:]
	}
	if t == "" || t[0] == ' ' || t[0] == '\t' {
		return false
	}
	i := strings.IndexByte(t, ':')
	if i <= 0 {
		return false
	}
	for _, r := range t[:i] {
		if !(r >= 'a' && r <= 'z') && r != '_' {
			return false
		}
	}
	return true
}

// MergeBindingStubs adds a commented binding stub to the config at path for every name
// in names that the bindings section does not already mention. A name already present is
// skipped whether its entry is commented or live, so the call is idempotent and never
// disturbs a binding the operator has already filled in. It returns the names added.
func MergeBindingStubs(path string, names []string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")

	// A config can carry more than one bindings section, typically a live one added
	// below the commented template. Collect the names from every section, so a name
	// bound outside the first one is still recognised as present.
	var heads []int
	for i, l := range lines {
		if bindingsHeadingRe.MatchString(l) {
			heads = append(heads, i)
		}
	}
	if len(heads) == 0 {
		return nil, fmt.Errorf("no bindings section found in %s", path)
	}

	sectionEnd := func(h int) int {
		for i := h + 1; i < len(lines); i++ {
			if isTopLevelKey(lines[i]) {
				return i
			}
		}
		return len(lines)
	}

	present := map[string]bool{}
	for _, h := range heads {
		for _, l := range lines[h+1 : sectionEnd(h)] {
			if m := bindingStubNameRe.FindStringSubmatch(l); m != nil {
				present[m[1]] = true
			}
		}
	}

	// New stubs go in the first section, which is the template the operator edits.
	start, end := heads[0], sectionEnd(heads[0])

	var added []string
	var stubs []string
	for _, n := range names {
		if present[n] {
			continue
		}
		added = append(added, n)
		stubs = append(stubs, "  # "+n+":", "  #   - host: <hostname>")
	}
	if len(added) == 0 {
		return nil, nil
	}

	// Insert after the last indented line of the block. Scanning back from the next
	// top-level key instead would land inside that key's own comment block.
	at := start + 1
	for i := start + 1; i < end; i++ {
		if bindingBlockLineRe.MatchString(lines[i]) {
			at = i + 1
		}
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, stubs...)
	out = append(out, lines[at:]...)

	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return nil, err
	}
	return added, nil
}

// missing_bindings modes.
const (
	MissingBindingsIgnore = "ignore"
	MissingBindingsWarn   = "warn"
	MissingBindingsFail   = "fail"
)

// MissingBindingsMode returns the configured startup check, defaulting to warn. A
// secret with no binding is denied either way; this only decides whether the operator
// is told, because a silent denial surfaces later as an unexplained 403.
func (c *ProxyConfig) MissingBindingsMode() (string, error) {
	switch c.MissingBindings {
	case "":
		return MissingBindingsWarn, nil
	case MissingBindingsIgnore, MissingBindingsWarn, MissingBindingsFail:
		return c.MissingBindings, nil
	default:
		return "", fmt.Errorf("missing_bindings must be ignore, warn or fail, got %q", c.MissingBindings)
	}
}

// SyncBindings reports whether to keep the bindings section in step on each start.
func (c *ProxyConfig) SyncBindings() bool {
	return c.BindingSync == nil || *c.BindingSync
}

// unboundExempt reports whether a secret is expected to have no binding.
// Doppler's own metadata values are never injected anywhere, and a pass_by_value
// secret is handed to the agent directly rather than brokered, so neither is a
// missing binding.
func (c *ProxyConfig) unboundExempt(name string) bool {
	if strings.HasPrefix(name, "DOPPLER_") {
		return true
	}
	return slices.Contains(c.PassByValue, name)
}

// SecretsWithoutBinding returns the names, in the order given, that carry no active
// binding and are not exempt. A commented stub does not count: it leaves the secret
// refused everywhere, which is exactly what the check reports.
func (c *ProxyConfig) SecretsWithoutBinding(names []string) []string {
	var out []string
	for _, n := range names {
		if c.unboundExempt(n) || len(c.Bindings[n]) > 0 {
			continue
		}
		out = append(out, n)
	}
	return out
}

// SetMissingBindings rewrites the unbound_report line in the config at path, leaving the
// rest of the file byte-identical so the operator's comments and edits survive.
func SetMissingBindings(path, mode string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "missing_bindings:") {
			lines[i] = "missing_bindings: " + mode
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("no missing_bindings line found in %s", path)
}

// PruneBindingStubs removes the commented stub for every secret in the bindings section
// that is absent from names. It only ever removes a commented stub: a binding the
// operator has uncommented is a destination they chose, so a secret that disappears
// from the Doppler config for any reason still leaves that line untouched. Returns the
// names removed.
func PruneBindingStubs(path string, names []string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(names))
	for _, n := range names {
		live[n] = true
	}
	lines := strings.Split(string(data), "\n")

	var heads []int
	for i, l := range lines {
		if bindingsHeadingRe.MatchString(l) {
			heads = append(heads, i)
		}
	}
	if len(heads) == 0 {
		return nil, fmt.Errorf("no bindings section found in %s", path)
	}
	sectionEnd := func(h int) int {
		for i := h + 1; i < len(lines); i++ {
			if isTopLevelKey(lines[i]) {
				return i
			}
		}
		return len(lines)
	}

	drop := make(map[int]bool)
	var removed []string
	for _, h := range heads {
		end := sectionEnd(h)
		for i := h + 1; i < end; i++ {
			m := bindingStubNameRe.FindStringSubmatch(lines[i])
			if m == nil || live[m[1]] {
				continue
			}
			// Only a commented stub is ever removed.
			if !strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
				continue
			}
			removed = append(removed, m[1])
			drop[i] = true
			// Take the stub's own indented continuation lines with it.
			for j := i + 1; j < end; j++ {
				if bindingStubNameRe.MatchString(lines[j]) || !bindingBlockLineRe.MatchString(lines[j]) {
					break
				}
				drop[j] = true
			}
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if !drop[i] {
			out = append(out, l)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return nil, err
	}
	return removed, nil
}
