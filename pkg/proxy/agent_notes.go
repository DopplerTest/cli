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
	_ "embed"
	"sort"
	"strings"
)

// The notes the agent is handed at the start of every run live in sandbox_notes.md so
// the prose is reviewed as prose. They say enough to do real work accurately, and
// nothing that helps an agent misbehave: what a placeholder is and how to use one,
// which destination each secret may reach, what a refusal looks like and the one
// correct response to it, and where files persist.
//
// Deliberately absent: the passthrough list (the hosts the proxy does not inspect are a
// map to the exit), how injection or scrubbing work, the proxy address or credential,
// and any vocabulary of ways around the sandbox. The firewall enforces those; the notes
// should not advertise them. TestSandboxNotesNeverHelpAnAgentMisbehave holds that line.
//
//go:embed sandbox_notes.md
var sandboxNotesTemplate string

const bindingsPlaceholder = "{{bindings}}"

// RenderSandboxNotes fills the template's one slot, the allowed destination for each
// bound secret in name order, from the agent's resolved proxy config.
func RenderSandboxNotes(cfg *ProxyConfig) string {
	return strings.Replace(sandboxNotesTemplate, bindingsPlaceholder, renderBindings(cfg), 1)
}

func renderBindings(cfg *ProxyConfig) string {
	names := make([]string, 0, len(cfg.Bindings))
	for name, rules := range cfg.Bindings {
		if len(rules) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "No secret has an allowed destination yet."
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		hosts := make([]string, 0, len(cfg.Bindings[name]))
		for _, r := range cfg.Bindings[name] {
			hosts = append(hosts, r.Host)
		}
		b.WriteString("- " + name + ": " + strings.Join(hosts, ", ") + "\n")
	}
	b.WriteString("\nAny other secret in your environment has no allowed destination yet.")
	return b.String()
}
