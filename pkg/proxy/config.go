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
	"os"

	"gopkg.in/yaml.v3"
)

// ProxyConfig is the user-editable proxy configuration (doppler-proxy.yaml).
type ProxyConfig struct {
	// ListenAddress is the address the proxy binds. Defaults (via the starter
	// config) to 0.0.0.0:14322 so the `doppler agent run` sandbox can reach it.
	// The --address flag overrides this.
	ListenAddress string `yaml:"listen_address"`

	// Passthrough lists hostnames the proxy blind-tunnels instead of
	// intercepting (no TLS termination, no injection).
	Passthrough []string `yaml:"passthrough"`

	// Intercept lists extra hostnames the Envoy engine (--engine envoy) should
	// MITM, beyond those it infers from token shapes. It's the escape hatch for
	// secrets whose values aren't recognizable tokens. Ignored by the default
	// masked-hash engine, which intercepts every host automatically.
	Intercept []string `yaml:"intercept"`

	// Signing declares hosts the Envoy engine should sign with AWS SigV4 instead of
	// masking. Envoy holds the AWS credentials (from its own environment).
	Signing []SigningRoute `yaml:"signing"`

	// OAuth declares hosts the Envoy engine should inject an OAuth2 client-
	// credentials Bearer token into. Envoy runs the grant and holds the token.
	OAuth []OAuthRoute `yaml:"oauth"`
}

// SigningRoute configures AWS SigV4 signing for a host (Envoy engine only).
type SigningRoute struct {
	Host    string `yaml:"host"`    // e.g. sts.amazonaws.com
	Service string `yaml:"service"` // e.g. sts
	Region  string `yaml:"region"`  // e.g. us-east-1
}

// OAuthRoute configures OAuth2 credential injection for a host (Envoy engine only).
type OAuthRoute struct {
	Host          string `yaml:"host"`           // e.g. api.spotify.com
	TokenEndpoint string `yaml:"token_endpoint"` // e.g. https://accounts.spotify.com/api/token
	ClientID      string `yaml:"client_id"`
	// ClientSecretRef names a Doppler secret whose VALUE is the OAuth client secret.
	ClientSecretRef string   `yaml:"client_secret_ref"`
	Scopes          []string `yaml:"scopes"`
}

// starterConfig is written on first run so the operator has an editable file,
// pre-filled with sensible defaults (an AI agent's control-plane is passed
// through so its own traffic isn't intercepted).
const starterConfig = `# doppler-proxy.yaml — configuration for the Doppler agent credential proxy.
# Edit this file, then restart the proxy to apply changes.

# Address the proxy listens on. 0.0.0.0 lets the ` + "`doppler agent run`" + ` sandbox
# container reach it; change to 127.0.0.1 to bind loopback only. --address overrides.
listen_address: 0.0.0.0:14322

# Hosts the proxy BLIND-TUNNELS instead of intercepting: no TLS termination and
# no credential injection. Put an agent's own control-plane here so its traffic
# passes through untouched (e.g. an AI agent reaching its model provider). This
# must include the agent's AUTH domains too — intercepting them breaks login
# (auth endpoints reject an unexpected CA), so Claude's login/session domains are
# passed through alongside its model endpoint.
passthrough:
  - api.anthropic.com
  - console.anthropic.com
  - claude.ai
  - claude.com
  - statsig.anthropic.com
  - sentry.io

# Hosts the Envoy engine (--engine envoy) additionally INTERCEPTS, on top of the
# ones it infers from your secrets' token shapes (e.g. ghp_… -> api.github.com).
# Add API hosts whose secrets aren't recognizable tokens. Only the envoy engine
# reads this; the default masked-hash engine intercepts every host automatically.
# intercept:
#   - api.internal.example.com

# Most setups need NOTHING below — the envoy engine auto-configures from your
# Doppler secret NAMES:
#   ghp_... value                                -> masked injection to github
#   AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY    -> AWS SigV4 signing to STS
#                                                   (region from AWS_REGION, else us-east-1)
#   SPOTIFY_CLIENT_ID + SPOTIFY_CLIENT_SECRET    -> OAuth2 injection to api.spotify.com
# The blocks below are ONLY for hosts/providers that aren't auto-detected.

# Extra AWS services beyond the auto-signed STS default:
# signing:
#   - host: s3.amazonaws.com
#     service: s3
#     region: us-east-1

# OAuth providers not built in (client_secret_ref names a Doppler secret):
# oauth:
#   - host: api.example.com
#     token_endpoint: https://auth.example.com/oauth/token
#     client_id: your-client-id
#     client_secret_ref: EXAMPLE_CLIENT_SECRET
`

// LoadOrScaffold loads the proxy config from path. If the file does not exist it
// writes the starter config and returns it with created=true.
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

// MergeIntercept returns the config's intercept hosts plus any extras,
// de-duplicated and order-preserving (config entries first).
func MergeIntercept(cfg *ProxyConfig, extra []string) []string {
	return mergeHostLists(cfg.Intercept, extra)
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
