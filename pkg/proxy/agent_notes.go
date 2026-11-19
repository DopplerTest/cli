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
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The notes the agent is handed at the start of every run live in agent_notes.md so
// the prose is reviewed as prose. They say enough to do real work accurately, and
// nothing that helps an agent misbehave: what a placeholder is and how to use one,
// which destination each secret may reach, what a refusal looks like and the one
// correct response to it, and where files persist.
//
// Deliberately absent: the passthrough list (the hosts the proxy does not inspect are a
// map to the exit), how injection or scrubbing work, the proxy address or credential,
// and any vocabulary of ways around the sandbox. The firewall enforces those; the notes
// should not advertise them. TestAgentNotesNeverHelpAnAgentMisbehave holds that line.
//
//go:embed agent_notes.md
var agentNotesTemplate string

const bindingsPlaceholder = "{{bindings}}"

// RenderAgentNotes fills the template's one slot, the allowed destination for each
// bound secret in name order, from the agent's resolved proxy config.
func RenderAgentNotes(cfg *ProxyConfig) string {
	return strings.Replace(agentNotesTemplate, bindingsPlaceholder, renderBindings(cfg), 1)
}

func renderBindings(cfg *ProxyConfig) string {
	names := make([]string, 0, len(cfg.Bindings))
	for name, rules := range cfg.Bindings {
		if len(rules) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "No secret has an allowed destination yet. Each is allowed on first use, by the user."
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
	b.WriteString("\nAny other secret in your environment has no allowed destination yet; the user is asked on first use.")
	return b.String()
}

// Claude Code takes text for its system prompt with claudeNotesFlag, or from a file
// with claudeNotesFileFlag. It refuses a command that has both.
const (
	claudeNotesFlag     = "--append-system-prompt"
	claudeNotesFileFlag = "--append-system-prompt-file"
)

// PromptFile is a file of system prompt text an agent run writes for Claude Code.
type PromptFile struct {
	Path    string
	Content []byte
}

// CommandWithNotes returns the command with the notes given to the agent as an
// argument. Only Claude Code takes them this way, so any other command is returned as
// it is. The flag goes straight after the program, because Claude Code refuses it after
// a subcommand. A command that already sets the flag keeps its one flag and gets the
// notes after its own text, in the same argument.
//
// A command that names a file of text with claudeNotesFileFlag gets no flag added,
// since Claude Code refuses the two together. The file it names is replaced by one for
// the caller to write into dataDir: the text of the user's file and then the notes.
// When the user's file cannot be read, the command is returned as it is, with the
// error.
func CommandWithNotes(command []string, notes, dataDir string, readFile func(string) ([]byte, error)) ([]string, []PromptFile, error) {
	if len(command) == 0 || notes == "" || filepath.Base(command[0]) != "claude" {
		return command, nil, nil
	}
	withNotes := slices.Clone(command)
	var files []PromptFile
	hasFlag := false
	for i := 1; i < len(withNotes); i++ {
		flag, value, inOneArgument := strings.Cut(withNotes[i], "=")
		if flag != claudeNotesFlag && flag != claudeNotesFileFlag {
			continue
		}
		prefix := flag + "="
		if !inOneArgument {
			if i+1 == len(withNotes) {
				break
			}
			i++
			prefix, value = "", withNotes[i]
		}
		if flag == claudeNotesFileFlag {
			text, err := readFile(value)
			if err != nil {
				return command, nil, err
			}
			content := []byte(strings.TrimRight(string(text), "\n") + "\n\n" + notes)
			files = append(files, PromptFile{Path: contentPath(dataDir, "append-system-prompt", ".md", content), Content: content})
			value = files[len(files)-1].Path
		} else {
			value += "\n\n" + notes
		}
		withNotes[i] = prefix + value
		hasFlag = true
	}
	if hasFlag {
		return withNotes, files, nil
	}
	return slices.Concat(command[:1], []string{claudeNotesFlag, notes}, command[1:]), nil, nil
}
