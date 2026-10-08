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
	"fmt"
	"path/filepath"
	"strings"
)

// Every run starts with a short POSIX sh prelude inside the sandbox, run as the sandbox
// user, that prepares the agent's home before exec'ing the real command. It delivers the
// sandbox notes (see RenderSandboxNotes), merges them into the agent's own instruction
// file when we know where that is, and spares Claude its first-run wizard when a token
// is supplied. The notes ride in as DOPPLER_SANDBOX_NOTES, named to docker the same way
// credentials are so they never appear in the command line, and the variable is removed
// before the agent starts.

// SandboxNotesEnv is the variable the prelude reads the notes from.
const SandboxNotesEnv = "DOPPLER_SANDBOX_NOTES"

// Where each known agent reads standing instructions from, relative to $HOME.
var instructionFiles = map[string]string{
	"claude": ".claude/CLAUDE.md",
	"codex":  ".codex/AGENTS.md",
	"gemini": ".gemini/GEMINI.md",
}

const notesPrelude = `if [ -n "$DOPPLER_SANDBOX_NOTES" ]; then
  printf '%s\n' "$DOPPLER_SANDBOX_NOTES" > "$HOME/DOPPLER_SANDBOX.md"
fi
`

// Replaces the previous run's fenced block and appends the current one, so the allowed
// destinations are always current and anything the user put in the file is untouched.
const mergePreludeFmt = `if [ -n "$DOPPLER_SANDBOX_NOTES" ]; then
  f="$HOME/%s"
  mkdir -p "$(dirname "$f")"
  [ -f "$f" ] || : > "$f"
  awk '/<!-- doppler-sandbox:start -->/{skip=1} !skip{print} /<!-- doppler-sandbox:end -->/{skip=0}' "$f" > "$f.tmp" && mv "$f.tmp" "$f"
  printf '\n<!-- doppler-sandbox:start -->\n%%s\n<!-- doppler-sandbox:end -->\n' "$DOPPLER_SANDBOX_NOTES" >> "$f"
fi
`

// Claude Code shows its onboarding wizard, ending in a browser login, whenever the home
// directory has never completed onboarding. The login cannot finish inside the sandbox,
// and an env token satisfies the API without ever setting the completed flag, so the
// wizard came back on every run. This only ever adds to ~/.claude.json and never
// rewrites a file it cannot parse and merge.
const claudeOnboardingPrelude = `f="$HOME/.claude.json"
if [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ] && ! grep -q '"hasCompletedOnboarding"[[:space:]]*:[[:space:]]*true' "$f" 2>/dev/null; then
  if [ ! -s "$f" ]; then
    printf '{"hasCompletedOnboarding":true,"theme":"dark"}\n' > "$f"
  elif command -v node >/dev/null 2>&1; then
    node -e 'const fs=require("fs"),p=process.argv[1];let d={};try{d=JSON.parse(fs.readFileSync(p,"utf8"))}catch{}d.hasCompletedOnboarding=true;d.theme=d.theme||"dark";fs.writeFileSync(p,JSON.stringify(d,null,2))' "$f"
  fi
fi
`

// WrapRun returns the command to run in the sandbox: the prelude for this agent, then
// an exec of the original command with its arguments intact. An empty command is
// returned unchanged.
func WrapRun(command []string, envKeys []string) []string {
	if len(command) == 0 {
		return command
	}
	program := filepath.Base(command[0])
	var b strings.Builder
	b.WriteString(notesPrelude)
	if file, ok := instructionFiles[program]; ok {
		b.WriteString(fmt.Sprintf(mergePreludeFmt, file))
	}
	if program == "claude" && contains(envKeys, "CLAUDE_CODE_OAUTH_TOKEN") {
		b.WriteString(claudeOnboardingPrelude)
	}
	b.WriteString("unset DOPPLER_SANDBOX_NOTES\nexec \"$@\"\n")
	return append([]string{"sh", "-c", b.String(), "doppler-run"}, command...)
}
