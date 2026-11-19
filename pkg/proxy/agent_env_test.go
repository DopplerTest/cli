/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	agentproxy "github.com/DopplerTest/agent-proxy"
)

func TestAgentEnvRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SetAgentEnv(dir, "CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-abc"); err != nil {
		t.Fatal(err)
	}
	// A value containing '=' must survive: only the first '=' separates key from value.
	if err := SetAgentEnv(dir, "OPENAI_API_KEY", "sk-x==y"); err != nil {
		t.Fatal(err)
	}

	env, err := ReadAgentEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-abc", "OPENAI_API_KEY": "sk-x==y"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("ReadAgentEnv = %v, want %v", env, want)
	}
	if got := AgentEnvKeys(dir); !reflect.DeepEqual(got, []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY"}) {
		t.Fatalf("AgentEnvKeys = %v, want sorted names only", got)
	}

	info, err := os.Stat(AgentLocalEnvPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode = %o, want 0600", info.Mode().Perm())
	}

	if err := UnsetAgentEnv(dir, "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetAgentEnv(dir, "NOT_THERE"); err != nil {
		t.Fatalf("unsetting a missing key should not error: %v", err)
	}
	env, _ = ReadAgentEnv(dir)
	if _, ok := env["OPENAI_API_KEY"]; ok || env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-abc" {
		t.Fatalf("after unset: %v", env)
	}
}

func TestAgentEnvRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"", "lower", "1STARTS_WITH_DIGIT", "HAS-DASH", "HAS SPACE", "HAS=EQUALS"} {
		if err := SetAgentEnv(dir, key, "v"); err == nil {
			t.Errorf("key %q should be rejected", key)
		}
	}
	if err := SetAgentEnv(dir, "OK_KEY", "line1\nline2"); err == nil {
		t.Error("a value containing a newline should be rejected (it would corrupt the file)")
	}
	if err := SetAgentEnv(dir, "OK_KEY", ""); err == nil {
		t.Error("an empty value should be rejected")
	}
}

func TestReadAgentEnvMissingIsEmpty(t *testing.T) {
	env, err := ReadAgentEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("expected empty map for a missing file, got %v", env)
	}
	if keys := AgentEnvKeys(t.TempDir()); len(keys) != 0 {
		t.Fatalf("expected no keys, got %v", keys)
	}
}

func TestExportAgentEnvSetsProcessEnvAndReturnsNamesOnly(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("ZZ_TEST_B"); os.Unsetenv("ZZ_TEST_A") })
	names := ExportAgentEnv(map[string]string{"ZZ_TEST_B": "two", "ZZ_TEST_A": "one"})
	// Names only, sorted: these go to docker as `-e NAME` so the value is inherited
	// from this process's environment and never appears in argv.
	if !reflect.DeepEqual(names, []string{"ZZ_TEST_A", "ZZ_TEST_B"}) {
		t.Fatalf("ExportAgentEnv = %v, want sorted names", names)
	}
	if os.Getenv("ZZ_TEST_A") != "one" || os.Getenv("ZZ_TEST_B") != "two" {
		t.Fatal("values should be exported into the process environment")
	}
}

func TestSharedEnvRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	if err := SetSharedEnv(configDir, "CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-shared"); err != nil {
		t.Fatal(err)
	}
	if err := SetSharedEnv(configDir, "OPENAI_API_KEY", "sk-x==y"); err != nil {
		t.Fatal(err)
	}

	env, err := ReadSharedEnv(configDir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-shared", "OPENAI_API_KEY": "sk-x==y"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("ReadSharedEnv = %v, want %v", env, want)
	}
	if got := SharedEnvKeys(configDir); !reflect.DeepEqual(got, []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY"}) {
		t.Fatalf("SharedEnvKeys = %v, want sorted names only", got)
	}

	// The shared file sits at the top of the config directory, beside the agents
	// directory rather than inside any agent's, with the same 0600 mode.
	if got, want := SharedEnvPath(configDir), filepath.Join(configDir, "env"); got != want {
		t.Fatalf("SharedEnvPath = %q, want %q", got, want)
	}
	info, err := os.Stat(SharedEnvPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("shared env file mode = %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(AgentLocalEnvPath(AgentDir(configDir, "demo"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a shared set must not touch an agent's own file: %v", err)
	}

	if err := UnsetSharedEnv(configDir, "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetSharedEnv(configDir, "NOT_THERE"); err != nil {
		t.Fatalf("unsetting a missing key should not error: %v", err)
	}
	env, _ = ReadSharedEnv(configDir)
	if _, ok := env["OPENAI_API_KEY"]; ok || env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-shared" {
		t.Fatalf("after unset: %v", env)
	}
}

func TestSharedEnvRejectsBadInput(t *testing.T) {
	configDir := t.TempDir()
	if err := SetSharedEnv(configDir, "lower", "v"); err == nil {
		t.Error("a bad key should be rejected for the shared file too")
	}
	if err := SetSharedEnv(configDir, "OK_KEY", ""); err == nil {
		t.Error("an empty value should be rejected for the shared file too")
	}
}

func TestReadSharedEnvMissingIsEmpty(t *testing.T) {
	env, err := ReadSharedEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("expected empty map for a missing file, got %v", env)
	}
	if keys := SharedEnvKeys(t.TempDir()); len(keys) != 0 {
		t.Fatalf("expected no keys, got %v", keys)
	}
}

func TestMergeAgentEnvAgentWins(t *testing.T) {
	agent := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "agent-token", "AGENT_ONLY": "a"}
	shared := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "shared-token", "SHARED_ONLY": "s"}
	got := MergeAgentEnv(agent, shared)
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "agent-token", "AGENT_ONLY": "a", "SHARED_ONLY": "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeAgentEnv = %v, want %v", got, want)
	}
	// Neither input is changed by the merge.
	if agent["SHARED_ONLY"] != "" || shared["CLAUDE_CODE_OAUTH_TOKEN"] != "shared-token" {
		t.Fatal("MergeAgentEnv must not write into its inputs")
	}
	if got := MergeAgentEnv(nil, nil); len(got) != 0 {
		t.Fatalf("merging two empty environments = %v, want empty", got)
	}
}

// The sandbox defaults keep an AWS SDK or the aws CLI off aws-chunked uploads, which
// the proxy cannot sign, and give way to a value the user stores themselves.
func TestRunDefaultsSitUnderSharedAndAgentEnv(t *testing.T) {
	if got := RunDefaults["AWS_REQUEST_CHECKSUM_CALCULATION"]; got != "when_required" {
		t.Fatalf("AWS_REQUEST_CHECKSUM_CALCULATION = %q, want when_required", got)
	}
	shared := map[string]string{"AWS_REQUEST_CHECKSUM_CALCULATION": "when_supported"}
	if got := MergeAgentEnv(nil, MergeAgentEnv(shared, RunDefaults)); got["AWS_REQUEST_CHECKSUM_CALCULATION"] != "when_supported" {
		t.Fatalf("a shared value must override the default; got %v", got)
	}
	agent := map[string]string{"AWS_REQUEST_CHECKSUM_CALCULATION": "when_supported"}
	if got := MergeAgentEnv(agent, MergeAgentEnv(nil, RunDefaults)); got["AWS_REQUEST_CHECKSUM_CALCULATION"] != "when_supported" {
		t.Fatalf("the agent's own value must override the default; got %v", got)
	}
}

// The region set on the agent's AWS signing pairs reaches the sandbox, so the aws CLI
// and SDKs do not go looking for one at the instance metadata endpoint.
func TestAWSRegionEnvCarriesThePairsRegion(t *testing.T) {
	pair := func(region string) CredentialMethod {
		return CredentialMethod{Kind: "aws_sigv4", Region: region, AccessKeyID: "AWS_ACCESS_KEY_ID"}
	}
	for _, tc := range []struct {
		name    string
		methods map[string]CredentialMethod
		want    map[string]string
	}{
		{"one pair", map[string]CredentialMethod{"AWS_SECRET_ACCESS_KEY": pair("us-west-2")},
			map[string]string{"AWS_REGION": "us-west-2", "AWS_DEFAULT_REGION": "us-west-2"}},
		{"pairs that agree", map[string]CredentialMethod{"A": pair("eu-west-1"), "B": pair("eu-west-1"), "C": pair("")},
			map[string]string{"AWS_REGION": "eu-west-1", "AWS_DEFAULT_REGION": "eu-west-1"}},
		{"pairs that disagree", map[string]CredentialMethod{"A": pair("eu-west-1"), "B": pair("us-east-2")}, nil},
		{"no region", map[string]CredentialMethod{"AWS_SECRET_ACCESS_KEY": pair("")}, nil},
		{"other methods", map[string]CredentialMethod{"CLIENT_SECRET": {Kind: "oauth2_client_credentials", Region: "us-west-2"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AWSRegionEnv(&ProxyConfig{Methods: tc.methods}, nil); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AWSRegionEnv = %v, want %v", got, tc.want)
			}
		})
	}

	// A region the user stores, under either name, stands alone: AWS_REGION is read
	// first, so the pair's would otherwise override a stored AWS_DEFAULT_REGION.
	cfg := &ProxyConfig{Methods: map[string]CredentialMethod{"K": pair("us-west-2")}}
	for _, stored := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		if got := AWSRegionEnv(cfg, map[string]string{stored: "ap-south-1"}); got != nil {
			t.Fatalf("with %s stored, AWSRegionEnv = %v, want nothing", stored, got)
		}
	}
}

// A credential stored only in the shared file still reaches the run: the merged
// names are what `agent run` exports and hands to the prelude, so the Claude
// onboarding check must fire from the shared token alone.
func TestMergedSharedTokenReachesTheClaudePrelude(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("CLAUDE_CODE_OAUTH_TOKEN") })
	names := ExportAgentEnv(MergeAgentEnv(map[string]string{}, map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "shared-token"}))
	if !reflect.DeepEqual(names, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("exported names = %v, want the shared token's name", names)
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "shared-token" {
		t.Fatal("the shared value should be exported into the process environment")
	}
	script := scriptOf(t, WrapRun([]string{"claude"}, names, false))
	if !strings.Contains(script, "hasCompletedOnboarding") {
		t.Error("the Claude onboarding prelude should fire when the token comes from the shared file")
	}
}

// agentEnvQuote writes a value the way the proxy writes it to agent.env.
func agentEnvQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func TestParseAgentEnvReversesTheProxysQuoting(t *testing.T) {
	values := []struct{ name, value string }{
		{"HTTPS_PROXY", "http://doppler:tok@127.0.0.1:14330"},
		{"NODE_EXTRA_CA_CERTS", "/Users/dev/Library/Application Support/doppler/ca.crt"},
		{"STRIPE_KEY", "dp.mask.abc123"},
		{"QUOTED", "it's a 'quoted' value"},
		{"PEM", "-----BEGIN KEY-----\nline=one\n# not a comment\n-----END KEY-----"},
		{"WITH_EQUALS", "a=b=c"},
		{"EMPTY", ""},
	}
	content := "# Generated by the agent proxy. Source to configure an agent:\n#   set -a; source agent.env; set +a\n\n"
	var want []string
	for _, v := range values {
		content += v.name + "=" + agentEnvQuote(v.value) + "\n"
		want = append(want, v.name+"="+v.value)
	}
	if got := ParseAgentEnv(content); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAgentEnv = %q, want %q", got, want)
	}
}

func TestParseAgentEnvReadsAnUnquotedValueAndAFileWithNoFinalNewline(t *testing.T) {
	got := ParseAgentEnv("PLAIN=value\nLAST='end'")
	if want := []string{"PLAIN=value", "LAST=end"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAgentEnv = %q, want %q", got, want)
	}
}

// environOf reads an environment the way os/exec does: the last entry for a name wins.
func environOf(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		out[name] = value
	}
	return out
}

func TestRunEnvLaysTheProxysEntriesOverTheUsersOwn(t *testing.T) {
	env := environOf(RunEnv{
		Base:     []string{"PATH=/usr/bin", "HTTPS_PROXY=http://corp:3128", "STRIPE_KEY=sk_live_real"},
		AgentEnv: []string{"HTTPS_PROXY=http://doppler:tok@127.0.0.1:14330", "SSL_CERT_FILE=/data/ca.crt", "STRIPE_KEY=dp.mask.abc123"},
		Config:   &ProxyConfig{},
	}.Environ())
	for name, want := range map[string]string{
		"PATH":          "/usr/bin",
		"HTTPS_PROXY":   "http://doppler:tok@127.0.0.1:14330",
		"SSL_CERT_FILE": "/data/ca.crt",
		"STRIPE_KEY":    "dp.mask.abc123",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
}

// A bundle variable names the only roots a client trusts, so its file holds the system's
// roots, the roots the user added and then the proxy CA, each certificate once.
func TestCAFilesAddTheProxyCAToTheRootsTheUserAlreadyTrusts(t *testing.T) {
	certificate := func(name string) string {
		return "-----BEGIN CERTIFICATE-----\n" + name + "\n-----END CERTIFICATE-----\n"
	}
	// A file is named by the first six bytes of the SHA-256 hash of its content.
	fileOf := func(name string, variables []string, certificates ...string) CAFile {
		content := ""
		for _, subject := range certificates {
			content += certificate(subject)
		}
		hash := sha256.Sum256([]byte(content))
		return CAFile{filepath.Join("/data", fmt.Sprintf("%s-%x.crt", name, hash[:6])), []byte(content), variables}
	}
	proxyCAPath := agentproxy.CACertPath("/data")
	bundleOf := func(certificates ...string) []CAFile {
		return []CAFile{fileOf("ca-bundle", []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}, certificates...)}
	}
	nodeExtraOf := func(certificates ...string) CAFile {
		return fileOf("node-extra-ca-certs", []string{"NODE_EXTRA_CA_CERTS"}, certificates...)
	}
	outerBundle, outerNodeExtra := bundleOf("macos", "more macos", "corp", "proxy")[0].Path, nodeExtraOf("node", "proxy").Path
	for _, tc := range []struct {
		name  string
		env   []string
		files map[string]string
		want  []CAFile
	}{
		{
			name:  "the system's roots when the user names none",
			env:   []string{"PATH=/usr/bin"},
			files: map[string]string{"/etc/ssl/certs/ca-certificates.crt": certificate("debian"), "/etc/pki/tls/certs/ca-bundle.crt": certificate("fedora")},
			want:  bundleOf("debian", "proxy"),
		},
		{
			name:  "the system's roots for a user who names a bundle for git alone",
			env:   []string{"GIT_SSL_CAINFO=/corp/git.pem"},
			files: map[string]string{"/etc/ssl/cert.pem": certificate("macos"), "/corp/git.pem": certificate("git")},
			want:  bundleOf("macos", "git", "proxy"),
		},
		{
			name:  "each file the user names, once",
			env:   []string{"GIT_SSL_CAINFO=/corp/git.pem", "CURL_CA_BUNDLE=/corp/roots.pem", "REQUESTS_CA_BUNDLE=/corp/gone.pem", "SSL_CERT_FILE=/corp/roots.pem"},
			files: map[string]string{"/etc/ssl/cert.pem": certificate("macos"), "/corp/roots.pem": certificate("corp"), "/corp/git.pem": certificate("git")},
			want:  bundleOf("macos", "corp", "git", "proxy"),
		},
		{
			name:  "the certificates of a file and nothing else in it",
			env:   []string{"SSL_CERT_FILE=/corp/roots.pem"},
			files: map[string]string{"/corp/roots.pem": "# Corp root\n" + certificate("corp") + "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n"},
			want:  bundleOf("corp", "proxy"),
		},
		{
			name: "the proxy CA alone when there are no roots to read",
			env:  []string{"PATH=/usr/bin"},
			want: bundleOf("proxy"),
		},
		{
			name: "each certificate once for a run started inside another run",
			env:  []string{"SSL_CERT_FILE=" + outerBundle, "NODE_EXTRA_CA_CERTS=" + outerNodeExtra},
			files: map[string]string{
				"/etc/ssl/cert.pem": certificate("macos") + certificate("more macos"),
				outerBundle:         certificate("macos") + certificate("more macos") + certificate("corp") + certificate("proxy"),
				outerNodeExtra:      certificate("node") + certificate("proxy"),
			},
			want: append(bundleOf("macos", "more macos", "corp", "proxy"), nodeExtraOf("node", "proxy")),
		},
		{
			name:  "the proxy's own ca.crt in the user's variables is not a file of theirs",
			env:   []string{"SSL_CERT_FILE=" + proxyCAPath, "GIT_SSL_CAINFO=" + proxyCAPath, "NODE_EXTRA_CA_CERTS=" + proxyCAPath},
			files: map[string]string{"/etc/ssl/cert.pem": certificate("macos"), proxyCAPath: certificate("an older proxy")},
			want:  bundleOf("macos", "proxy"),
		},
		{
			name:  "a second file for the roots the user adds to Node's",
			env:   []string{"NODE_EXTRA_CA_CERTS=/corp/node.pem"},
			files: map[string]string{"/corp/node.pem": certificate("node"), "/etc/ssl/cert.pem": certificate("macos")},
			want:  append(bundleOf("macos", "proxy"), nodeExtraOf("node", "proxy")),
		},
		{
			name:  "no second file when Node could not read the user's own",
			env:   []string{"NODE_EXTRA_CA_CERTS=/corp/gone.pem"},
			files: map[string]string{"/etc/ssl/cert.pem": certificate("macos")},
			want:  bundleOf("macos", "proxy"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readFile := func(path string) ([]byte, error) {
				content, ok := tc.files[path]
				if !ok {
					return nil, os.ErrNotExist
				}
				return []byte(content), nil
			}
			got := CAFiles(tc.env, []byte(certificate("proxy")), "/data", readFile)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CAFiles = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two runs of one agent can be going at the same time, each started with its own
// environment. A file one of them replaced would change the roots of the other, so runs
// that trust different roots get different files.
func TestCAFilesGiveRunsThatTrustDifferentRootsDifferentFiles(t *testing.T) {
	certificate := func(name string) []byte {
		return []byte("-----BEGIN CERTIFICATE-----\n" + name + "\n-----END CERTIFICATE-----\n")
	}
	readFile := func(path string) ([]byte, error) { return certificate(path), nil }
	filesOf := func(roots string) []CAFile {
		return CAFiles([]string{"SSL_CERT_FILE=" + roots, "NODE_EXTRA_CA_CERTS=" + roots}, certificate("proxy"), "/data", readFile)
	}
	first, same, other := filesOf("/corp/one.pem"), filesOf("/corp/one.pem"), filesOf("/corp/two.pem")
	for i, name := range []string{"ca-bundle", "node-extra-ca-certs"} {
		if !regexp.MustCompile(`^/data/` + name + `-[0-9a-f]{12}\.crt$`).MatchString(first[i].Path) {
			t.Errorf("%s is written to %q, want a file of /data with a hash in its name", name, first[i].Path)
		}
		if first[i].Path != same[i].Path {
			t.Errorf("%s: two runs with the same roots write %q and %q, want one file", name, first[i].Path, same[i].Path)
		}
		if first[i].Path == other[i].Path {
			t.Errorf("%s: two runs with different roots both write %q, want a file each", name, first[i].Path)
		}
	}
}

// agent.env points every CA variable at the proxy's ca.crt. The files written for the
// run take their variables over, and a variable with no file keeps the proxy's entry.
func TestRunEnvPointsTheCAVariablesAtTheFilesOfTheRun(t *testing.T) {
	env := environOf(RunEnv{
		Base:     []string{"SSL_CERT_FILE=/corp/roots.pem"},
		AgentEnv: []string{"SSL_CERT_FILE=/data/ca.crt", "GIT_SSL_CAINFO=/data/ca.crt", "NODE_EXTRA_CA_CERTS=/data/ca.crt"},
		CAFiles:  []CAFile{{Path: "/data/ca-bundle.crt", Variables: []string{"SSL_CERT_FILE", "GIT_SSL_CAINFO"}}},
		Config:   &ProxyConfig{},
	}.Environ())
	for name, want := range map[string]string{
		"SSL_CERT_FILE":       "/data/ca-bundle.crt",
		"GIT_SSL_CAINFO":      "/data/ca-bundle.crt",
		"NODE_EXTRA_CA_CERTS": "/data/ca.crt",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
}

// curl reads https_proxy before HTTPS_PROXY, so the user's own lower-case value would
// send its requests past the Doppler proxy.
func TestRunEnvPointsBothSpellingsOfTheProxyVariablesAtTheProxy(t *testing.T) {
	env := environOf(RunEnv{
		Base:     []string{"https_proxy=http://corp:3128", "http_proxy=http://corp:3128", "all_proxy=socks5://corp:1080"},
		AgentEnv: []string{"HTTPS_PROXY=http://doppler:tok@127.0.0.1:14330", "HTTP_PROXY=http://doppler:tok@127.0.0.1:14330"},
		Config:   &ProxyConfig{},
	}.Environ())
	for name, want := range map[string]string{
		"HTTPS_PROXY": "http://doppler:tok@127.0.0.1:14330",
		"https_proxy": "http://doppler:tok@127.0.0.1:14330",
		"HTTP_PROXY":  "http://doppler:tok@127.0.0.1:14330",
		"http_proxy":  "http://doppler:tok@127.0.0.1:14330",
		"all_proxy":   "socks5://corp:1080",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
}

// With the user's Doppler token the agent could read the real secrets from the API.
func TestRunEnvDropsTheUsersDopplerToken(t *testing.T) {
	base := []string{"PATH=/usr/bin", "DOPPLER_TOKEN=dp.st.real"}
	env := environOf(RunEnv{Base: base, Config: &ProxyConfig{}}.Environ())
	if token, ok := env["DOPPLER_TOKEN"]; ok || env["PATH"] != "/usr/bin" {
		t.Errorf("DOPPLER_TOKEN = %q, PATH = %q, want the token dropped and the rest kept", token, env["PATH"])
	}

	// A secret of that name which the user passes by value comes from the proxy.
	env = environOf(RunEnv{Base: base, AgentEnv: []string{"DOPPLER_TOKEN=dp.st.by_value"}, Config: &ProxyConfig{}}.Environ())
	if env["DOPPLER_TOKEN"] != "dp.st.by_value" {
		t.Errorf("DOPPLER_TOKEN = %q, want the proxy's own entry, %q", env["DOPPLER_TOKEN"], "dp.st.by_value")
	}
}

func TestRunEnvAddsTheDefaultsTheUserHasNotSet(t *testing.T) {
	cfg := &ProxyConfig{Methods: map[string]CredentialMethod{
		"AWS_SECRET_ACCESS_KEY": {Kind: "aws_sigv4", Region: "us-west-2", AccessKeyID: "AWS_ACCESS_KEY_ID"},
	}}
	env := environOf(RunEnv{Base: []string{"PATH=/usr/bin"}, Config: cfg}.Environ())
	for name, want := range map[string]string{
		"AWS_REQUEST_CHECKSUM_CALCULATION": "when_required",
		"AWS_REGION":                       "us-west-2",
		"AWS_DEFAULT_REGION":               "us-west-2",
		"NODE_USE_ENV_PROXY":               "1",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}

	// A value from the user's own environment, or from the proxy, stands.
	env = environOf(RunEnv{
		Base:     []string{"AWS_REQUEST_CHECKSUM_CALCULATION=when_supported", "NODE_USE_ENV_PROXY=0"},
		AgentEnv: []string{"AWS_DEFAULT_REGION=ap-south-1"},
		Config:   cfg,
	}.Environ())
	for name, want := range map[string]string{
		"AWS_REQUEST_CHECKSUM_CALCULATION": "when_supported",
		"AWS_DEFAULT_REGION":               "ap-south-1",
		"NODE_USE_ENV_PROXY":               "0",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want the value already set, %q", name, env[name], want)
		}
	}
	if region, ok := env["AWS_REGION"]; ok {
		t.Errorf("AWS_REGION = %q, want it left unset beside a stored AWS_DEFAULT_REGION", region)
	}
}

// Requests to this machine stay off the proxy, which refuses loopback destinations,
// and the entries the user already had are kept.
func TestRunEnvKeepsLoopbackOffTheProxy(t *testing.T) {
	for _, tc := range []struct {
		name string
		base []string
		want string
	}{
		{"nothing set", nil, "localhost,127.0.0.1,::1"},
		{"the user's entries come first", []string{"NO_PROXY=corp.internal, 10.0.0.0/8"}, "corp.internal,10.0.0.0/8,localhost,127.0.0.1,::1"},
		{"an entry is listed once", []string{"NO_PROXY=localhost,corp.internal", "no_proxy=corp.internal,127.0.0.1"}, "localhost,corp.internal,127.0.0.1,::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := environOf(RunEnv{Base: tc.base, Config: &ProxyConfig{}}.Environ())
			if env["NO_PROXY"] != tc.want || env["no_proxy"] != tc.want {
				t.Fatalf("NO_PROXY = %q, no_proxy = %q, want both %q", env["NO_PROXY"], env["no_proxy"], tc.want)
			}
		})
	}
}
