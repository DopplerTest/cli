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

// A resume reopens the agent's latest conversation for the working directory, which
// each program keeps under the home volume, so it survives a sandbox restart. The
// prelude decides inside the sandbox, where the home is mounted and the layout can be
// checked with a file test, and only adds the program's resume flag when there is a
// conversation to continue. Asking the program to resume with nothing on disk ends the
// run with an error, which is what this avoids. A command that already resumes is left
// alone.
//
// Claude Code keeps one folder per working directory under ~/.claude/projects, named by
// the path with every character outside [A-Za-z0-9] turned into "-". Codex writes one
// JSONL file per session under ~/.codex/sessions with the working directory in its
// header. Gemini CLI keeps chats under ~/.gemini/tmp/<sha256 of the directory>/chats.
var resumePreludes = map[string]string{
	"claude": `if ls "$HOME/.claude/projects/$(printf '%s' "$PWD" | sed 's/[^A-Za-z0-9]/-/g')"/*.jsonl >/dev/null 2>&1; then
  case " $* " in *" --continue "*|*" -c "*|*" --resume "*|*" -r "*) ;; *) set -- "$@" --continue ;; esac
  doppler_resumed=1
fi
`,
	"codex": `if grep -rlsF --include='*.jsonl' "\"cwd\":\"$PWD\"" "$HOME/.codex/sessions" 2>/dev/null | grep -q .; then
  if [ "$2" != "resume" ]; then doppler_prog=$1; shift; set -- "$doppler_prog" resume --last "$@"; fi
  doppler_resumed=1
fi
`,
	"gemini": `if ls "$HOME/.gemini/tmp/$(printf '%s' "$PWD" | sha256sum | cut -d' ' -f1)/chats"/*.json >/dev/null 2>&1; then
  case " $* " in *" --resume "*|*" -r "*) ;; *) set -- "$@" --resume ;; esac
  doppler_resumed=1
fi
`,
}

// Says which way the run went, so the user watching the terminal is not left guessing
// why the agent came up empty.
const resumeOutcomePrelude = `if [ -n "$doppler_resumed" ]; then
  echo "Continuing the previous conversation."
else
  echo "No previous conversation here, starting a new one."
fi
`

// WrapRun returns the command to run in the sandbox: the prelude for this agent, then
// an exec of the original command with its arguments intact. With resume set, the
// program's latest conversation for the working directory is reopened when it has one.
// An empty command is returned unchanged.
func WrapRun(command []string, envKeys []string, resume bool) []string {
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
	if resume {
		b.WriteString(resumePreludes[program])
		b.WriteString(resumeOutcomePrelude)
	}
	b.WriteString("unset DOPPLER_SANDBOX_NOTES\nexec \"$@\"\n")
	return append([]string{"sh", "-c", b.String(), "doppler-run"}, command...)
}
