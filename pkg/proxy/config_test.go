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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentproxy "github.com/DopplerTest/agent-proxy"
)

// The scaffold ships commented examples and no operator secret names: it is written
// once, so seeding names here would freeze the list at whatever existed on first run.
// `proxy bindings` fills them in on demand instead.
func TestScaffoldShipsCommentedExamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, created, err := LoadOrScaffold(path)
	if err != nil || !created {
		t.Fatalf("scaffold: created=%v err=%v", created, err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"#   EXAMPLE_TOKEN:", "binding_sync: true"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("scaffolded config missing %q\n%s", want, data)
		}
	}
	if len(cfg.Bindings) != 0 {
		t.Errorf("scaffolded examples must be commented (inactive), got bindings %v", cfg.Bindings)
	}
}

// Re-running the merge is a no-op, and a name already present is skipped whether its
// entry is commented or live.
func TestMergeBindingStubs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	// The scaffolded bindings section is empty, so every name is new. The example in
	// the header comment sits outside the section and is not treated as a binding.
	added, err := MergeBindingStubs(path, []string{"TEST_PROXY", "DATABASE_URL"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(added, []string{"TEST_PROXY", "DATABASE_URL"}) {
		t.Fatalf("added = %v, want [TEST_PROXY DATABASE_URL]", added)
	}
	// The stubs are commented, so nothing is bound yet.
	cfg, _, err := LoadOrScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings) != 0 {
		t.Fatalf("merged stubs must be inactive, got %v", cfg.Bindings)
	}
	// Anchor on structure, not on comment wording: the stub must sit directly under the
	// bindings heading or another line of the block, never after the blank line that
	// separates the section from the next key's comment.
	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	stubAt := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "  # TEST_PROXY:") {
			stubAt = i
		}
	}
	if stubAt <= 0 {
		t.Fatalf("stub not found\n%s", data)
	}
	prev := lines[stubAt-1]
	if prev != "bindings:" && !strings.HasPrefix(prev, "  ") {
		t.Fatalf("stub landed outside the bindings block, preceded by %q\n%s", prev, data)
	}

	// Idempotent.
	again, err := MergeBindingStubs(path, []string{"TEST_PROXY", "DATABASE_URL"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second merge should add nothing, added %v", again)
	}
}

// A live (uncommented) binding counts as present, so the merge leaves it alone.
func TestMergeBindingStubsSkipsLiveEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	live := strings.Replace(string(data), "bindings:", "bindings:\n  TEST_PROXY:\n    - host: api.example.com", 1)
	if err := os.WriteFile(path, []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := MergeBindingStubs(path, []string{"TEST_PROXY"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("a live binding must be skipped, added %v", added)
	}
	cfg, _, err := LoadOrScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings["TEST_PROXY"]) != 1 {
		t.Fatalf("the live binding was disturbed: %v", cfg.Bindings)
	}
}

func TestLoadOrScaffold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	cfg, created, err := LoadOrScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected the config to be scaffolded on first run")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file was not written: %v", err)
	}
	if !slices.Contains(cfg.Passthrough, "api.anthropic.com") {
		t.Fatalf("starter config missing api.anthropic.com; got %v", cfg.Passthrough)
	}
	if cfg.ListenAddress != "0.0.0.0:14322" {
		t.Fatalf("starter config listen_address = %q, want 0.0.0.0:14322", cfg.ListenAddress)
	}

	// A second load reads the existing file — not scaffolded again.
	cfg2, created2, err := LoadOrScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("expected created=false when the file already exists")
	}
	if !slices.Equal(cfg.Passthrough, cfg2.Passthrough) {
		t.Fatal("passthrough changed across reloads")
	}
}

// ENG-9723: the scaffolded passthrough list is a set of blind holes — no audit, no
// injection — so it must stay minimal. A third-party error sink (sentry.io) or
// telemetry (statsig.anthropic.com) must not be blind-tunneled: they work fine
// intercepted, and a Sentry DSN is a world-writable exfil endpoint.
func TestScaffoldedPassthroughDropsTelemetryHoles(t *testing.T) {
	cfg, err := parseProxyConfig([]byte(starterConfig))
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]string{
		"sentry.io":             "a third-party, world-writable error sink",
		"statsig.anthropic.com": "telemetry",
	}
	for _, h := range cfg.Passthrough {
		if why, bad := banned[h]; bad {
			t.Errorf("passthrough must not blind-tunnel %q (%s) — it works intercepted", h, why)
		}
	}
	if !slices.Contains(cfg.Passthrough, "api.anthropic.com") {
		t.Error("api.anthropic.com must remain — the agent cannot function without its model API")
	}
}

func TestLoadOrScaffoldRewritesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Pre-create an empty (blank) file — the bug case.
	if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, created, err := LoadOrScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("an empty file should be (re)scaffolded, created=true")
	}
	if !slices.Contains(cfg.Passthrough, "api.anthropic.com") {
		t.Fatalf("scaffolded config not populated; got %v", cfg.Passthrough)
	}
	data, _ := os.ReadFile(path)
	if len(data) == 0 {
		t.Fatal("file is still empty after scaffold")
	}
}

func TestMergePassthrough(t *testing.T) {
	cfg := &ProxyConfig{Passthrough: []string{"a.com", "b.com"}}
	got := MergePassthrough(cfg, []string{"b.com", "c.com", ""})
	want := []string{"a.com", "b.com", "c.com"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParsePassthroughList(t *testing.T) {
	cfg, err := parseProxyConfig([]byte("passthrough:\n  - a.com\n  - b.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Passthrough, []string{"a.com", "b.com"}) {
		t.Fatalf("passthrough = %v", cfg.Passthrough)
	}
}

func TestParseBindings(t *testing.T) {
	cfg, err := parseProxyConfig([]byte(`
bindings:
  GITHUB_TOKEN:
    - host: api.github.com
      paths: ["/repos/**"]
      methods: [GET]
  STRIPE_KEY:
    - host: api.stripe.com
unbound: trust-first-use
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings) != 2 {
		t.Fatalf("bindings = %v", cfg.Bindings)
	}
	gh := cfg.Bindings["GITHUB_TOKEN"]
	if len(gh) != 1 || gh[0].Host != "api.github.com" || !slices.Equal(gh[0].Paths, []string{"/repos/**"}) || !slices.Equal(gh[0].Methods, []string{"GET"}) {
		t.Fatalf("GITHUB_TOKEN rules = %+v", gh)
	}
	if cfg.Unbound != "trust-first-use" {
		t.Fatalf("unbound = %q", cfg.Unbound)
	}
	if _, err := cfg.BindingResolver(); err != nil {
		t.Fatal(err)
	}
}

// With no bindings block at all, an unrecognizable secret is refused everywhere.
func TestBindingResolverDefaultsToDeny(t *testing.T) {
	r, err := (&ProxyConfig{}).BindingResolver()
	if err != nil {
		t.Fatal(err)
	}
	ok, why := r.Allowed(agentproxy.BindingRequest{
		Name:  "DB_PASSWORD",
		Value: "plain-database-password",
		Dest:  agentproxy.Destination{Host: "db.example.com:443", Path: "/", Method: "GET"},
	})
	if ok {
		t.Fatal("an undeclared secret must be refused by default")
	}
	if why == "" {
		t.Fatal("refusal should carry a reason")
	}
}

func TestBindingResolverRejectsUnknownPolicy(t *testing.T) {
	if _, err := (&ProxyConfig{Unbound: "maybe"}).BindingResolver(); err == nil {
		t.Fatal("an unknown unbound policy must be an error")
	}
}

func TestParseMethods(t *testing.T) {
	cfg, err := parseProxyConfig([]byte(`
methods:
  OAUTH_SECRET:
    kind: oauth2_client_credentials
    token_url: https://provider.example.com/oauth/token
    client_id: cid
    scopes: [read, write]
  AWS_SECRET_ACCESS_KEY:
    kind: aws_sigv4
    service: s3
    region: us-west-2
    access_key_id: AWS_ACCESS_KEY_ID
`))
	if err != nil {
		t.Fatal(err)
	}
	o := cfg.Methods["OAUTH_SECRET"]
	if o.Kind != "oauth2_client_credentials" || o.TokenURL != "https://provider.example.com/oauth/token" || o.ClientID != "cid" || !slices.Equal(o.Scopes, []string{"read", "write"}) {
		t.Fatalf("oauth method = %+v", o)
	}
	a := cfg.Methods["AWS_SECRET_ACCESS_KEY"]
	if a.Kind != "aws_sigv4" || a.Service != "s3" || a.Region != "us-west-2" || a.AccessKeyID != "AWS_ACCESS_KEY_ID" {
		t.Fatalf("sigv4 method = %+v", a)
	}
	// snake_case yaml maps cleanly to the agent-proxy method registry.
	m := cfg.MethodConfigs()
	if m["OAUTH_SECRET"].TokenURL != "https://provider.example.com/oauth/token" || m["AWS_SECRET_ACCESS_KEY"].AccessKeyID != "AWS_ACCESS_KEY_ID" {
		t.Fatalf("MethodConfigs mapping wrong: %+v", m)
	}
}

func TestMethodConfigsNilWhenEmpty(t *testing.T) {
	if got := (&ProxyConfig{}).MethodConfigs(); got != nil {
		t.Fatalf("expected nil methods when none declared, got %v", got)
	}
}

func TestParsePassByValue(t *testing.T) {
	cfg, err := parseProxyConfig([]byte("pass_by_value:\n  - MODEL_TOKEN\n  - OTHER\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.PassByValue, []string{"MODEL_TOKEN", "OTHER"}) {
		t.Fatalf("pass_by_value = %v", cfg.PassByValue)
	}
}

func TestAllowProtocolUpgrades(t *testing.T) {
	for _, tc := range []struct {
		value string
		allow bool
		bad   bool
	}{
		{value: "", allow: false},
		{value: "refuse", allow: false},
		{value: "tunnel", allow: true},
		{value: "yes", bad: true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			allow, err := (&ProxyConfig{ProtocolUpgrades: tc.value}).AllowProtocolUpgrades()
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), "refuse or tunnel") {
					t.Fatalf("expected a named-values error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if allow != tc.allow {
				t.Fatalf("allow = %v, want %v", allow, tc.allow)
			}
		})
	}
}

// The scaffold has to document the knob, commented out at its safe default.
func TestStarterConfigDocumentsProtocolUpgrades(t *testing.T) {
	if !strings.Contains(starterConfig, "\nprotocol_upgrades: refuse") {
		t.Fatal("scaffolded config does not document protocol_upgrades")
	}
}

// Every default is written as a live line, so the file states the behavior rather than
// leaving the operator to infer it from a commented example.
func TestStarterConfigStatesDefaultsExplicitly(t *testing.T) {
	cfg, err := parseProxyConfig([]byte(starterConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Unbound != "deny" {
		t.Errorf("unbound = %q, want deny", cfg.Unbound)
	}
	if cfg.ProtocolUpgrades != "refuse" {
		t.Errorf("protocol_upgrades = %q, want refuse", cfg.ProtocolUpgrades)
	}
	if cfg.MissingBindings != "warn" {
		t.Errorf("missing_bindings = %q, want warn", cfg.MissingBindings)
	}
	if cfg.ListenAddress != "0.0.0.0:14322" {
		t.Errorf("listen_address = %q", cfg.ListenAddress)
	}
}

// An unset missing_bindings defaults to warn, and a bad value is rejected at startup.
func TestMissingBindingsMode(t *testing.T) {
	for in, want := range map[string]string{"": "warn", "ignore": "ignore", "warn": "warn", "fail": "fail"} {
		got, err := (&ProxyConfig{MissingBindings: in}).MissingBindingsMode()
		if err != nil || got != want {
			t.Errorf("MissingBindingsMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := (&ProxyConfig{MissingBindings: "nope"}).MissingBindingsMode(); err == nil {
		t.Error("an unknown missing_bindings value must be rejected")
	}
}

// The check reports secrets that are actually refused. A commented stub does not count,
// and DOPPLER_* metadata plus pass_by_value secrets are never expected to have one.
func TestSecretsWithoutBinding(t *testing.T) {
	cfg := &ProxyConfig{
		Bindings:    map[string][]agentproxy.Rule{"GITHUB_TOKEN": {{Host: "api.github.com"}}},
		PassByValue: []string{"MODEL_PROVIDER_TOKEN"},
	}
	got := cfg.SecretsWithoutBinding([]string{
		"DOPPLER_CONFIG", "DOPPLER_PROJECT", "GITHUB_TOKEN", "MODEL_PROVIDER_TOKEN", "TEST_PROXY", "DATABASE_URL",
	})
	if !slices.Equal(got, []string{"TEST_PROXY", "DATABASE_URL"}) {
		t.Fatalf("SecretsWithoutBinding = %v, want [TEST_PROXY DATABASE_URL]", got)
	}
}

// A live bindings section added below the commented template is still a binding. The
// merge scans every section, so a name bound there is not re-added as a stub.
func TestMergeBindingStubsSeesLaterBindingsSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	// Drop the commented GITHUB_TOKEN example, then bind it for real further down.
	trimmed := string(data) + "\nbindings:\n  LATER_TOKEN:\n    - host: api.example.com\n"
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := MergeBindingStubs(path, []string{"LATER_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("a binding in a later section must be seen as present, added %v", added)
	}
}

// An override REPLACES a secret's rule list rather than merging with the base, so the
// effective allowlist is never a union the reader has to assemble.
func TestConfigOverrideReplacesRatherThanMerges(t *testing.T) {
	cfg, err := parseProxyConfig([]byte(`
bindings:
  GITHUB_TOKEN:
    - host: api.github.com
  STRIPE_SECRET_KEY:
    - host: api.stripe.com
missing_bindings: warn
unbound: deny
config_overrides:
  prd:
    bindings:
      GITHUB_TOKEN:
        - host: api.github.com
          paths: ["/repos/acme/**"]
    missing_bindings: fail
`))
	if err != nil {
		t.Fatal(err)
	}

	base, applied := cfg.ResolveForConfig("dev")
	if applied {
		t.Error("no override is defined for dev")
	}
	if len(base.Bindings["GITHUB_TOKEN"]) != 1 || len(base.Bindings["GITHUB_TOKEN"][0].Paths) != 0 {
		t.Errorf("dev should see the base rule unscoped, got %+v", base.Bindings["GITHUB_TOKEN"])
	}

	prd, applied := cfg.ResolveForConfig("prd")
	if !applied {
		t.Fatal("the prd override should apply")
	}
	// Replace, not merge: exactly one rule, the scoped one.
	got := prd.Bindings["GITHUB_TOKEN"]
	if len(got) != 1 {
		t.Fatalf("override must replace the rule list, got %d rules: %+v", len(got), got)
	}
	if len(got[0].Paths) != 1 || got[0].Paths[0] != "/repos/acme/**" {
		t.Errorf("prd rule = %+v, want the scoped path", got[0])
	}
	// A secret the override omits is inherited.
	if len(prd.Bindings["STRIPE_SECRET_KEY"]) != 1 {
		t.Error("an unnamed secret must inherit its base binding")
	}
	// A scalar the override sets wins; one it omits is inherited.
	if prd.MissingBindings != "fail" {
		t.Errorf("missing_bindings = %q, want fail", prd.MissingBindings)
	}
	if prd.Unbound != "deny" {
		t.Errorf("unbound = %q, want the inherited deny", prd.Unbound)
	}
	// Resolving must not mutate the base.
	if len(cfg.Bindings["GITHUB_TOKEN"][0].Paths) != 0 {
		t.Error("resolving an override mutated the base config")
	}
}

// Naming a secret with an empty rule list binds it nowhere, without disturbing the
// other secrets the override leaves alone.
func TestConfigOverrideEmptyRuleListBindsNothing(t *testing.T) {
	cfg, err := parseProxyConfig([]byte(`
bindings:
  GITHUB_TOKEN:
    - host: api.github.com
  STRIPE_SECRET_KEY:
    - host: api.stripe.com
config_overrides:
  locked:
    bindings:
      GITHUB_TOKEN: []
`))
	if err != nil {
		t.Fatal(err)
	}
	locked, _ := cfg.ResolveForConfig("locked")
	if len(locked.Bindings["GITHUB_TOKEN"]) != 0 {
		t.Errorf("an empty rule list must bind nothing, got %v", locked.Bindings["GITHUB_TOKEN"])
	}
	if len(locked.Bindings["STRIPE_SECRET_KEY"]) != 1 {
		t.Errorf("an untouched secret must keep its base binding, got %v", locked.Bindings["STRIPE_SECRET_KEY"])
	}
}

// SetMissingBindings rewrites only the one line, so comments and edits elsewhere survive.
func TestSetMissingBindingsPreservesTheRestOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := SetMissingBindings(path, MissingBindingsFail); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)

	cfg, err := parseProxyConfig(after)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MissingBindings != "fail" {
		t.Errorf("missing_bindings = %q, want fail", cfg.MissingBindings)
	}
	// Exactly one line differs.
	b, a := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	if len(b) != len(a) {
		t.Fatalf("line count changed: %d -> %d", len(b), len(a))
	}
	diff := 0
	for i := range b {
		if b[i] != a[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Errorf("%d lines changed, want exactly 1", diff)
	}
}

// The example lives in the header comment, above the bindings section, so binding_sync
// never prunes it. It used to sit inside the section and vanished on the first start.
func TestScaffoldExampleSurvivesSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeBindingStubs(path, []string{"REAL_SECRET"}); err != nil {
		t.Fatal(err)
	}
	removed, err := PruneBindingStubs(path, []string{"REAL_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("prune removed %v, want nothing", removed)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "EXAMPLE_TOKEN") {
		t.Errorf("the example must survive a sync\n%s", data)
	}
	if !strings.Contains(string(data), "  # REAL_SECRET:") {
		t.Errorf("the real stub should have been added\n%s", data)
	}
}
