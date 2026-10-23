import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

export const DEMO_ANTHROPIC_KEY = 'sk-ant-api03-attn-demo-0000000000000000000000';
export const DEMO_CODEX_KEY = 'sk-attn-demo';
export const DEMO_CODEX_MODEL = 'gpt-5.5-codex';

function installedVersion(binary) {
  return execFileSync(binary, ['--version'], { encoding: 'utf8' }).match(/\d+\.\d+\.\d+/)?.[0] ?? '';
}

function resolveBinary(name) {
  return execFileSync('/usr/bin/which', [name], { encoding: 'utf8' }).trim();
}

function writeClaudeHome(dir, trustedFolders) {
  const version = installedVersion(resolveBinary('claude'));
  fs.mkdirSync(dir, { recursive: true });
  const state = {
    hasCompletedOnboarding: true,
    lastOnboardingVersion: version,
    lastReleaseNotesSeen: version,
    hasSeenAutoDefaultNotice: true,
    autoModeClassifierBillingNoticeAcknowledgedAt: Date.now(),
    theme: 'dark',
    customApiKeyResponses: { approved: [DEMO_ANTHROPIC_KEY.slice(-20)], rejected: [] },
    projects: Object.fromEntries(trustedFolders.map((folder) => [folder, { hasTrustDialogAccepted: true, hasCompletedProjectOnboarding: true }])),
  };
  fs.writeFileSync(path.join(dir, '.claude.json'), `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
  fs.writeFileSync(path.join(dir, 'settings.json'), `${JSON.stringify({ permissions: { allow: ['Bash', 'Edit', 'Read', 'Write'] } }, null, 2)}\n`);
}

function codexProvider(origin) {
  return `{name="OpenAI",base_url="${origin}/v1",env_key="CODEX_API_KEY",wire_api="responses"}`;
}

function writeCodexHome(dir, trustedFolders, origin) {
  fs.mkdirSync(dir, { recursive: true });
  const projects = trustedFolders.map((folder) => `[projects."${folder}"]\ntrust_level = "trusted"\n`).join('\n');
  fs.writeFileSync(path.join(dir, 'config.toml'), `model = "${DEMO_CODEX_MODEL}"
model_provider = "demo"
check_for_update_on_startup = false

[model_providers]
demo = ${codexProvider(origin)}

${projects}`);
}

function writeCodexExecWrapper(binDir, origin) {
  fs.mkdirSync(binDir, { recursive: true });
  const codex = resolveBinary('codex');
  const wrapper = path.join(binDir, 'codex');
  fs.writeFileSync(wrapper, `#!/bin/sh
if [ "$1" = exec ]; then
  shift
  exec "${codex}" exec -c 'model_provider="demo"' -c 'model_providers.demo=${codexProvider(origin)}' "$@"
fi
exec "${codex}" "$@"
`, { mode: 0o755 });
  return wrapper;
}

function writeShellHome(toolHome) {
  const configDir = path.join(toolHome, 'config');
  const zshDir = path.join(toolHome, 'zsh');
  const nodeBin = path.dirname(process.execPath);
  fs.mkdirSync(path.join(configDir, 'fish'), { recursive: true });
  fs.mkdirSync(zshDir, { recursive: true });
  fs.writeFileSync(path.join(configDir, 'fish', 'config.fish'), `set -g fish_greeting
set -g fish_history ''
set -g fish_autosuggestion_enabled 0
fish_add_path --global ${nodeBin}
function fish_prompt
    set_color brblue; echo -n (string replace -r "^$HOME" "~" $PWD); set_color normal; echo -n ' $ '
end
`);
  fs.writeFileSync(path.join(zshDir, '.zshrc'), `path=(${nodeBin} $path)
PROMPT='%F{blue}%~%f $ '
`);
  return { XDG_CONFIG_HOME: configDir, ZDOTDIR: zshDir };
}

export function writeAgentHome({ toolHome, stubOrigin, trustedFolders }) {
  const claudeDir = path.join(toolHome, '.claude');
  const codexDir = path.join(toolHome, '.codex');
  writeClaudeHome(claudeDir, trustedFolders);
  writeCodexHome(codexDir, trustedFolders, stubOrigin);
  const codexWrapper = writeCodexExecWrapper(path.join(toolHome, 'bin'), stubOrigin);
  return {
    ...writeShellHome(toolHome),
    ATTN_TOOL_HOME: toolHome,
    CLAUDE_CONFIG_DIR: claudeDir,
    ANTHROPIC_BASE_URL: stubOrigin,
    ANTHROPIC_API_KEY: DEMO_ANTHROPIC_KEY,
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    DISABLE_AUTOUPDATER: '1',
    DISABLE_TELEMETRY: '1',
    DISABLE_ERROR_REPORTING: '1',
    CODEX_HOME: codexDir,
    CODEX_API_KEY: DEMO_CODEX_KEY,
    ATTN_CODEX_EXECUTABLE: codexWrapper,
  };
}
