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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	agentproxy "github.com/DopplerTest/agent-proxy"
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

// RunDefaults are the entries every agent run starts with, unless the environment the
// agent is given already sets them.
//
// AWS_REQUEST_CHECKSUM_CALCULATION: by default the AWS SDKs and CLI upload in
// aws-chunked bodies, signed chunk by chunk with the secret key the agent holds, which
// is a placeholder. The proxy cannot sign those and refuses them. when_required makes
// the SDKs send a whole body instead, which the proxy signs.
var RunDefaults = map[string]string{"AWS_REQUEST_CHECKSUM_CALCULATION": "when_required"}

// AWSRegionEnv is the region the config's AWS signing pairs name, as AWS_REGION and
// AWS_DEFAULT_REGION. Without one the AWS CLI and SDKs ask the instance metadata
// endpoint for a region, which a developer's machine does not have. Pairs that name
// different regions leave both unset, since no one default suits them all. So does a
// region env already holds under either name: the AWS tools read AWS_REGION first, so
// adding it would override an AWS_DEFAULT_REGION the user set.
func AWSRegionEnv(cfg *ProxyConfig, env map[string]string) map[string]string {
	if env["AWS_REGION"] != "" || env["AWS_DEFAULT_REGION"] != "" {
		return nil
	}
	region := ""
	for _, m := range cfg.Methods {
		if m.Kind != "aws_sigv4" || m.Region == "" {
			continue
		}
		if region != "" && region != m.Region {
			return nil
		}
		region = m.Region
	}
	if region == "" {
		return nil
	}
	return map[string]string{"AWS_REGION": region, "AWS_DEFAULT_REGION": region}
}

// ParseAgentEnv reads the proxy's agent.env into NAME=value entries. The proxy writes
// every value in single quotes, and a value may span lines. A quote inside a value is
// written the POSIX way:
//
//	NAME='it'\''s'
//
// This reverses that quoting and nothing more, so no shell is evaluated.
func ParseAgentEnv(content string) []string {
	var entries []string
	for rest := content; rest != ""; {
		line, _, _ := strings.Cut(rest, "\n")
		name, _, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			rest = rest[min(len(line)+1, len(rest)):]
			continue
		}
		value, read := unquoteValue(rest[len(name)+1:])
		entries = append(entries, strings.TrimSpace(name)+"="+value)
		rest = rest[len(name)+1+read:]
	}
	return entries
}

// unquoteValue reads one quoted value up to the end of its line. It returns the value
// and the number of bytes read, the newline included.
func unquoteValue(s string) (string, int) {
	var value strings.Builder
	i := 0
	for i < len(s) && s[i] != '\n' {
		switch {
		case s[i] == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				value.WriteString(s[i+1:])
				return value.String(), len(s)
			}
			value.WriteString(s[i+1 : i+1+end])
			i += end + 2
		case s[i] == '\\' && i+1 < len(s):
			value.WriteByte(s[i+1])
			i += 2
		default:
			value.WriteByte(s[i])
			i++
		}
	}
	return value.String(), min(i+1, len(s))
}

// RunEnv is what the environment of an agent run on the host is built from.
type RunEnv struct {
	// Base is the environment `agent run` itself was started with.
	Base []string
	// AgentEnv is the proxy's agent.env as ParseAgentEnv returns it: the proxy address,
	// the CA paths and a placeholder for each secret.
	AgentEnv []string
	// CAFiles are the files of trusted roots written for this run. Each of their
	// variables is pointed at its file, over the entry agent.env has for it.
	CAFiles []CAFile
	// Config is the agent's proxy config, empty without a named agent. It is read for
	// the region of its AWS signing pairs.
	Config *ProxyConfig
}

// CAFile is a file of trusted roots an agent run writes, and the variables that name it
// to the command.
type CAFile struct {
	Path      string
	Content   []byte
	Variables []string
}

// caBundleVars each name the only roots a client trusts. An OpenSSL client pointed at
// the proxy's CA alone could verify no connection the proxy does not intercept.
var caBundleVars = []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}

// nodeExtraCAVar names roots Node adds to its own, so its file needs no system roots.
const nodeExtraCAVar = "NODE_EXTRA_CA_CERTS"

// systemCABundles are where a system keeps its roots as one file.
var systemCABundles = []string{
	"/etc/ssl/cert.pem",                  // macOS
	"/etc/ssl/certs/ca-certificates.crt", // Debian, Ubuntu
	"/etc/pki/tls/certs/ca-bundle.crt",   // Fedora, RHEL
	"/etc/ssl/ca-bundle.pem",             // openSUSE
	"/etc/pki/tls/cacert.pem",            // OpenELEC
}

const (
	caBundleName     = "ca-bundle"
	nodeExtraCAsName = "node-extra-ca-certs"
)

// contentPath returns a path in dir for a file with this content: the name, a short
// hash of the content and the extension. What a run writes depends on the environment
// it was started with, and two runs of one agent can be going at the same time. Under
// one fixed name the second would replace the file the first still reads. Named by
// content, runs that write the same share a file and any other run gets its own.
func contentPath(dir, name, extension string, content []byte) string {
	hash := sha256.Sum256(content)
	return filepath.Join(dir, name+"-"+hex.EncodeToString(hash[:6])+extension)
}

// CAFiles returns the files of roots the command of an agent run trusts, to be written
// into dataDir. The bundle holds the system's roots, then the certificates of every
// file the user's own bundle variables name in env, then the proxy CA. A user who sets
// one of those variables for one tool still gets the system's roots in the others,
// since all of them are pointed at the bundle. With no roots to read the bundle holds
// the proxy CA alone. A NODE_EXTRA_CA_CERTS of the user's own gets a second file, their
// roots and the proxy CA, since the proxy's entry would replace theirs. Each file is
// named by its content, see contentPath.
func CAFiles(env []string, proxyCA []byte, dataDir string, readFile func(string) ([]byte, error)) []CAFile {
	set := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		set[name] = value
	}
	// The proxy's own ca.crt in a variable is not the user's choice of roots. It is
	// there because they loaded agent.env into their shell.
	usersFile := func(name string) string {
		if path := set[name]; path != "" && filepath.Clean(path) != agentproxy.CACertPath(dataDir) {
			return path
		}
		return ""
	}
	var roots [][]byte
	for _, path := range systemCABundles {
		if content, err := readFile(path); err == nil {
			roots = append(roots, content)
			break
		}
	}
	var named []string
	for _, name := range caBundleVars {
		path := usersFile(name)
		if path == "" || slices.Contains(named, path) {
			continue
		}
		named = append(named, path)
		if content, err := readFile(path); err == nil {
			roots = append(roots, content)
		}
	}
	bundle := joinCertificates(append(roots, proxyCA)...)
	files := []CAFile{{
		Path:      contentPath(dataDir, caBundleName, ".crt", bundle),
		Content:   bundle,
		Variables: caBundleVars,
	}}
	if path := usersFile(nodeExtraCAVar); path != "" {
		if extra, err := readFile(path); err == nil {
			nodeExtras := joinCertificates(extra, proxyCA)
			files = append(files, CAFile{
				Path:      contentPath(dataDir, nodeExtraCAsName, ".crt", nodeExtras),
				Content:   nodeExtras,
				Variables: []string{nodeExtraCAVar},
			})
		}
	}
	return files
}

var certificateRe = regexp.MustCompile(`(?s)-----BEGIN ([A-Z0-9]+ )?CERTIFICATE-----.*?-----END ([A-Z0-9]+ )?CERTIFICATE-----`)

// joinCertificates writes each certificate of the files once, in the order they come.
// A run started inside another run reads the bundle of the outer one as the user's own,
// and would otherwise write every certificate of it a second time. Nothing but the
// certificates is copied, so a private key kept in the same file stays where it is.
func joinCertificates(files ...[]byte) []byte {
	var joined []byte
	written := map[string]bool{}
	for _, file := range files {
		for _, certificate := range certificateRe.FindAll(file, -1) {
			if !written[string(certificate)] {
				written[string(certificate)] = true
				joined = append(append(joined, certificate...), '\n')
			}
		}
	}
	return joined
}

// loopbackNoProxy keeps requests to this machine off the proxy. The agent still has to
// reach a local dev server, and the proxy refuses loopback and plain-HTTP destinations.
const loopbackNoProxy = "localhost,127.0.0.1,::1"

// dopplerTokenEnv names the user's own Doppler token. An agent that held it could read
// the real secrets straight from the Doppler API, so Base never passes it on.
const dopplerTokenEnv = "DOPPLER_TOKEN"

// Environ returns the environment the agent starts with. A later entry wins over an
// earlier one of the same name, the way os/exec reads a duplicate. The proxy's entries
// win over Base, which loses its Doppler token, and a CA file wins over both. A default
// is added only where neither sets the name.
func (r RunEnv) Environ() []string {
	env := slices.DeleteFunc(slices.Clone(r.Base), func(entry string) bool {
		return strings.HasPrefix(entry, dopplerTokenEnv+"=")
	})
	for _, entry := range r.AgentEnv {
		env = append(env, entry)
		// curl and other tools read the lower-case name first, so a https_proxy of the
		// user's own would take their requests past the Doppler proxy.
		name, value, _ := strings.Cut(entry, "=")
		if name == "HTTPS_PROXY" || name == "HTTP_PROXY" {
			env = append(env, strings.ToLower(name)+"="+value)
		}
	}
	for _, file := range r.CAFiles {
		for _, name := range file.Variables {
			env = append(env, name+"="+file.Path)
		}
	}
	set := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		set[name] = value
	}
	addUnset := func(defaults map[string]string) {
		for _, name := range sortedEnvKeys(defaults) {
			if set[name] == "" {
				env = append(env, name+"="+defaults[name])
			}
		}
	}
	addUnset(RunDefaults)
	addUnset(AWSRegionEnv(r.Config, set))
	// Node's built-in fetch ignores HTTPS_PROXY without this, so a tool the agent
	// starts would send a placeholder straight to the real host.
	addUnset(map[string]string{"NODE_USE_ENV_PROXY": "1"})
	noProxy := withLoopback(set["NO_PROXY"], set["no_proxy"])
	return append(env, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
}

// withLoopback joins the user's own NO_PROXY entries and the loopback hosts, each once.
func withLoopback(upper, lower string) string {
	var hosts []string
	for _, entry := range strings.Split(upper+","+lower+","+loopbackNoProxy, ",") {
		host := strings.TrimSpace(entry)
		if host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	return strings.Join(hosts, ",")
}

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
