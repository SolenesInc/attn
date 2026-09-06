# Pi execution security

Pi sessions launched by attn sandbox built-in tools and filter credentials.
These protections stay active whichever reviewer `/auto` selects.

`/security` covers the built-in file tools, the read and write deny lists, and
the guidance each turn carries. A bash command runs under the daemon's
`sandbox_mode` instead, which the app's Settings owns; the two cover different
tools and do not overrule each other. [automode.md](automode.md) describes the
approval path a bash command walks.

`/security` opens a keyboard-driven settings panel in Pi. Use the arrow keys
and Enter to toggle the sandbox, tool networking and build-cache access. Open
a path list to add, edit or remove entries. Escape goes back or closes the
panel; Escape in a path editor cancels that edit. Changes apply immediately
and persist for future sessions. Other running sessions keep their current
policy.

The panel shows why an unavailable cache was skipped and identifies built-in
protections. Restore the standard cache preset from Cache directories.
Credential filtering stays on independently of the sandbox and cannot be
disabled from the panel.

`/security status` shows the effective paths, network mode, and settings file
as text. Bare `/security` also returns text outside Pi's terminal UI.
Settings live in `~/.pi/agent/attn-security.json`, or Pi's configured agent
directory. Project settings cannot override this file.

| Command | Effect |
| --- | --- |
| `/security allow-write /absolute/path` | Adds a persistent grant for an existing directory |
| `/security revoke-write /absolute/path` | Removes that additional grant |
| `/security protect-read /absolute/path` | Adds a protected read path |
| `/security unprotect-read /absolute/path` | Removes a configured read protection |
| `/security protect-write /absolute/path` | Adds a protected write path |
| `/security unprotect-write /absolute/path` | Removes a configured write protection; built-in protections remain |
| `/security caches off` | Disables all preset and customized build-cache grants |
| `/security caches on` | Enables the configured build-cache grants |
| `/security caches add /absolute/path` | Adds a cache directory, creating it if needed |
| `/security caches remove /absolute/path` | Removes a cache directory from the preset |
| `/security caches reset` | Restores and enables the standard cache preset |
| `/security network deny` | Blocks tool network access, including localhost |
| `/security network allow` | Restores unrestricted tool networking |
| `/security off` | Disables execution containment; filtering remains active |
| `/security on` | Restores execution containment |

The sandbox permits writes in the session directory, validated Git worktree metadata, and a
private temporary directory. Standard build-cache directories are also writable
by default, so ordinary builds never need an escalation for their caches.
Reads are generally allowed, except for SSH, AWS, GPG and Google Cloud credential
directories, Git credential files, and Pi's agent directory. The settings file
and project `.pi` and `.agents` directories are protected against tool writes.
Explicit denies take precedence over write grants. Protected reads and writes
are editable in `/security` or the `denyRead` and `denyWrite` settings arrays.
The settings file and project `.pi` and `.agents` write protections remain
in force while the sandbox is on.

## Build-cache preset

The default paths cover Go build and module caches, npm, Bun, pnpm stores,
Yarn, Corepack, pip, uv, Bazel/Bazelisk (including repository and disk caches),
Maven repositories, and Gradle caches, daemon state, native libraries and
wrapper distributions. Paths use the platform's conventional locations; custom
tool environment variables do not silently grant new directories. The full list
is visible in `/security status` and can be changed with the commands above.

Pi creates missing cache directories when applying an enabled policy. A path
that is explicitly protected, inaccessible, or redirected through a symlink is
skipped and reported in `/security status`. Configure the real directory for a
symlinked cache. Other tools continue working. Cache grants never grant their
parent directories or override protected descendants.

Settings keep the cache policy separate from explicit `allowWrite` grants:

```json
{
  "buildCaches": {
    "enabled": true,
    "paths": ["~/Library/Caches/go-build", "~/go/pkg/mod", "~/.npm"]
  }
}
```

Omitting `buildCaches` uses the enabled standard preset, including for existing
settings. Setting `enabled` to `false` disables it; an empty `paths` list removes
all cache entries. Turning it off or removing a path preserves cached files and
independent explicit write grants. These controls apply to the current session
immediately and persist for later sessions.

The cache grants are writable roots, so routine cache and lock writes by build,
test and dependency commands never reach a reviewer for the path itself. What
the command does still walks the approval path. Writable caches may contain
code used by later builds; disabling the preset removes those default grants.

## Agent recovery

Blocked native file operations explain the path and the policy that refused
access. Each agent turn receives the current writable paths, cache grants and
network policy. A shell command that fails with a permission error includes the
active write roots. Likely DNS, connection and download failures say so when
tool networking is blocked. Shell errors can also come from ordinary file
permissions, so the message does not claim every permission error proves a
sandbox denial.

The sandbox cannot be widened from inside a file-tool call. The agent is told to
work within it or to name the exact path and reason so you can decide; you grant
a directory with `/security allow-write <directory>`. There is no request the
agent can submit to widen these tools, and the guidance says so rather than
pointing at one.

A bash command is the exception, and it has its own route: the agent re-issues
with `sandbox_permissions: "require_escalated"` and a justification, which goes
to the reviewer. See [automode.md](automode.md).

## Execution and filtering

Shell commands use macOS Seatbelt or Linux bubblewrap. Native file operations
run in a small worker under the same OS policy. Search also runs sandboxed.
The worker starts on first use and exits when the session closes or security
settings change. Linux requires `bwrap` and search requires `rg` on PATH.
Missing sandbox support or malformed settings block execution rather than
running the tool without protection.
On Linux, empty `.pi` and `.agents` directories are created when absent so
bubblewrap can protect them. A custom write-deny path that does not exist
protects its nearest existing parent until that path is created outside Pi.

Credential filtering removes sensitive environment variables from tool
subprocesses while keeping provider authentication in Pi. Recognized token
formats, known credential values, and complete PEM private keys are redacted
from text results, streamed shell output, saved shell output, Guardian
requests, and denial records. Lines longer than 64 KiB are withheld entirely
so truncation cannot expose part of a token. Images and encoded or otherwise
unrecognized secrets are not detected by these text filters.

`!` and `!!` commands use the same shell restrictions and filters. Custom
extensions and MCP servers remain trusted code outside the sandbox; final
result and model-request filtering does not contain their execution or their
own logging. A bash command's network goes through attn's proxy, which enforces
the host allow and deny lists; `/security network deny` blocks tool networking
outright, including localhost. Linux network isolation covers TCP/IP. Reachable
filesystem sockets, such as an SSH agent or Docker socket, remain trusted
services with their own authority.

The approval channel is deliberately out of reach: `ATTN_PI_TOKEN` and
`ATTN_PI_SUITE_SOCKET` are stripped from the environment a sandboxed command
gets, so the agent cannot reach the reviewer it is being reviewed by.

Outside attn, load the packaged extension with `pi -e /path/to/security.js`.
It runs on its own: without attn there is no daemon config, so there is no
approval policy, no proxy and no reviewer, and the user-managed grants above are
the whole story.

The macOS base policy comes from OpenAI Codex, and credential-format detection
uses adapted Gitleaks rules and public token formats. Their source references
and license notices ship in `notices/`. Linux mount construction follows the
[bubblewrap command interface](https://github.com/containers/bubblewrap/blob/bb3ff51ec60b40ebf0f51b33521967213f5d857e/bwrap.xml).
