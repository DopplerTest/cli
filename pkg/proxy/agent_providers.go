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

import "path/filepath"

// The hosts each known agent needs to reach its own provider: the API itself plus the
// account and login endpoints its CLI talks to. These are tunnelled rather than
// inspected, so an agent's credential, which is handed to it by value, reaches the
// provider untouched. A program not listed here starts with no passthrough hosts; the
// user adds them, or saving a credential in the app adds that provider's hosts.
var providerHosts = map[string][]string{
	"claude": {"api.anthropic.com", "console.anthropic.com", "claude.ai", "claude.com"},
	"codex":  {"api.openai.com", "auth.openai.com", "chatgpt.com"},
	"gemini": {"generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com", "oauth2.googleapis.com", "accounts.google.com"},
	"aider":  {"api.openai.com", "api.anthropic.com"},
}

// ProviderHosts returns the passthrough hosts for the agent a command runs, by the
// program's basename, or nil for a program the proxy does not know.
func ProviderHosts(command []string) []string {
	if len(command) == 0 {
		return nil
	}
	hosts := providerHosts[filepath.Base(command[0])]
	if hosts == nil {
		return nil
	}
	return append([]string(nil), hosts...)
}
