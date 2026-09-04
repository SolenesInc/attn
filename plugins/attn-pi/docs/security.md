# Pi execution security

Pi sessions launched by attn sandbox built-in tools and filter credentials.
These protections stay active when `/auto off` disables the permission classifier.

`/security` opens a keyboard-driven settings panel in Pi. Use the arrow keys
and Enter to toggle the sandbox, tool networking and build-cache access. Open
a path list to add, edit or remove entries. Escape goes back or closes the
panel; Escape in a path editor cancels that edit. Changes apply immediately
and persist for future sessions. Other running sessions keep their current
policy.

The panel shows why an unavailable cache was skipped, identifies built-in
protections, and explains when auto mode can review temporary access. Restore
the standard cache preset from Cache directories. Credential filtering stays
on independently of the sandbox and cannot be disabled from the panel.

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
by default, so ordinary builds do not need a sandbox escalation for their caches.
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

Auto mode receives the active cache paths from the executor. Routine cache and
lock writes by build, test and dependency commands need no separate consent for
those paths. Other effects, cache purges, credential access and deliberate cache
poisoning remain subject to normal review. A directory named by the agent alone
does not establish this exception. Writable caches may contain code used by
later builds; disabling the preset removes those default grants.

## Agent recovery

Blocked native file operations explain the path and the policy that refused
access. Each agent turn receives the current writable paths, cache grants,
network policy and reviewer availability. A shell command that fails with a
permission error includes the active write roots and an available recovery route.
Likely DNS, connection and download failures include network-request guidance
when tool networking is blocked. Shell errors can also
come from ordinary file permissions, so the message does not claim every
permission error proves a sandbox denial.

When auto-mode review is available, the agent submits a scoped request directly,
without first asking the user in chat. An OS error has not established a review
refusal. For an additional directory outside the configured grants:

```json
{
  "command": "go test ./...",
  "sandbox": {
    "allowWrite": ["/existing/build-cache"],
    "reason": "The test build writes compiled packages to this cache."
  }
}
```

Auto mode reviews the command, canonical directory paths, and reason together,
even when the command matches a normal allow pattern. Approval permits that
execution and its children to use the requested directories; it does not change
saved permissions or other calls. The requested directories must already exist.
For a new cache, use a project-local directory or ask the user to create the
external directory first. A request can also include `"network": "allow"` to
review temporary unrestricted networking. Read/write denies and credential
filtering remain active, including beneath a granted parent directory.

A refusal tells the agent what was refused, why, and whether the user's explicit
approval can be considered on another attempt. The agent explains the refusal
to the user and submits the same scoped request after their approval. The
classifier reviews the reply; it is not an automatic override of policy.
With auto mode off or no reviewer configured, extra access is refused with
instructions to ask the user. Ordinary tool execution remains sandboxed.
Standalone security has no auto-mode reviewer; use user-managed grants there.

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
from text results, streamed shell output, saved shell output, classifier
requests, and denial records. Lines longer than 64 KiB are withheld entirely
so truncation cannot expose part of a token. Images and encoded or otherwise
unrecognized secrets are not detected by these text filters.

`!` and `!!` commands use the same shell restrictions and filters. Custom
extensions and MCP servers remain trusted code outside the sandbox; final
result and model-request filtering does not contain their execution or their
own logging. Networking is unrestricted unless set to `deny`; there is no
hostname allowlist or implied protection against arbitrary exfiltration.
Linux network isolation covers TCP/IP. Reachable filesystem sockets, such as
an SSH agent or Docker socket, remain trusted services with their own authority.

Outside attn, load the packaged extension with `pi -e /path/to/security.js`.
It can be combined with `automode.js` and works without a classifier model.

The macOS base policy comes from OpenAI Codex, and credential-format detection
uses adapted Gitleaks rules and public token formats. Their source references
and license notices ship in `notices/`. Linux mount construction follows the
[bubblewrap command interface](https://github.com/containers/bubblewrap/blob/bb3ff51ec60b40ebf0f51b33521967213f5d857e/bwrap.xml).
