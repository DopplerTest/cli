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

// Some agents have a first-run step that a supplied credential should spare the user.
// Claude Code shows its onboarding wizard, ending in a browser login, whenever the home
// directory has never completed onboarding. The browser login cannot finish inside the
// sandbox, and an env token satisfies the API but never sets the "completed" flag, so
// the wizard came back on every run. When the agent has that token stored, the command
// is wrapped in a short prelude that marks onboarding complete, then execs the agent.
type firstRun struct {
	program string
	envKey  string
	script  string
}

// POSIX sh, run as the sandbox user with HOME set. It only ever adds to ~/.claude.json
// and never rewrites a file it cannot parse and merge.
const claudeOnboardingPrelude = `f="$HOME/.claude.json"
if [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ] && ! grep -q '"hasCompletedOnboarding"[[:space:]]*:[[:space:]]*true' "$f" 2>/dev/null; then
  if [ ! -s "$f" ]; then
    printf '{"hasCompletedOnboarding":true,"theme":"dark"}\n' > "$f"
  elif command -v node >/dev/null 2>&1; then
    node -e 'const fs=require("fs"),p=process.argv[1];let d={};try{d=JSON.parse(fs.readFileSync(p,"utf8"))}catch{}d.hasCompletedOnboarding=true;d.theme=d.theme||"dark";fs.writeFileSync(p,JSON.stringify(d,null,2))' "$f"
  fi
fi
exec "$@"
`

var firstRuns = []firstRun{
	{program: "claude", envKey: "CLAUDE_CODE_OAUTH_TOKEN", script: claudeOnboardingPrelude},
}

// WrapFirstRun returns the command to run in the sandbox: unchanged unless the program
// is a known agent and its credential is among the agent's stored env keys, in which
// case the agent's first-run prelude runs first and then execs the original command.
func WrapFirstRun(command []string, envKeys []string) []string {
	if len(command) == 0 {
		return command
	}
	program := filepath.Base(command[0])
	for _, fr := range firstRuns {
		if fr.program != program || !contains(envKeys, fr.envKey) {
			continue
		}
		return append([]string{"sh", "-c", fr.script, "doppler-first-run"}, command...)
	}
	return command
}
