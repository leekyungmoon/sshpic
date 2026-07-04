# sshpic Development Plan

## One-liner

**sshpic** — paste local screenshots into remote SSH coding agents.

## Product scope

`sshpic` is a public/open-source-ready CLI project for developers who run Codex CLI, Claude Code, or other terminal AI agents inside SSH sessions and need to paste local screenshots/images into those remote sessions.

The core problem is not image model support. The core problem is the SSH boundary: local clipboard image data does not automatically exist inside a remote terminal session.

## Non-negotiable UX requirement

The normal flow **must not** require typing commands like:

```bash
codex-img-clip --debug
sshpic clip
```

after every screenshot.

Required P0 UX:

1. User captures/copies an image locally.
2. User focuses an SSH/Codex/Claude terminal session.
3. User presses Cmd+V in the configured iTerm2 profile.
4. `sshpic` uploads the image to the remote host.
5. The remote image path is inserted into the active terminal input.
6. The user did not type an upload/debug command.

## Product principles

- No daemon by default in v0.1.
- No remote install.
- No cloud upload.
- No SSH config mutation by default.
- Dotfiles-friendly and reproducible.
- Secure by default: `umask 077`, remote file mode `0600`, no secret logging.
- macOS + iTerm2 first, but architecture must allow Linux and Windows providers later.

## Primary v0.1 support scope

| Platform / terminal | v0.1 status | Claim allowed |
|---|---:|---|
| macOS + iTerm2 | Supported P0 after E2E proof | Direct paste into active SSH terminal via configured shortcut |
| macOS Terminal.app | Experimental / roadmap | No support claim until verified |
| macOS Warp | Experimental / roadmap | No support claim until verified |
| macOS Ghostty | Experimental / roadmap | No support claim until verified |
| WezTerm / Kitty | Experimental / roadmap | Snippets only after verified |
| Linux / Ubuntu | Roadmap | Provider architecture only; no support claim |
| Windows / WSL | Roadmap | Provider architecture only; no support claim |

## Core architecture decision

Build `sshpic` as a Go CLI with a no-daemon direct-paste path powered by terminal keybindings.

The primary v0.1 UX is **not**:

```bash
sshpic clip
```

The primary v0.1 UX is:

```text
copy/capture image → press Cmd+V → remote path appears in SSH/Codex input
```

## Direct-paste contract

`sshpic paste` is the machine-facing primitive for terminal integrations.

Contract:

1. A terminal keybinding invokes `sshpic paste`; the user does not type it.
2. `sshpic paste` reads the local clipboard.
3. If an image exists, it writes a local temp image, uploads over SSH, verifies SHA256 when enabled, and emits exactly one insertable payload: the remote image path.
4. If only text exists, smart paste emits exactly the original text payload.
5. The terminal integration injects the emitted payload into the active terminal input.
6. No extra newline is inserted unless `insert_newline=true` is explicitly configured.
7. The inserted payload is data only: no shell commands, shell interpolation, debug text, or terminal control sequences.

Terminal-safe mode:

```bash
sshpic paste --output=payload
```

This mode must output only the text to insert. Diagnostics must use `--debug` or `--json`, never the terminal keybinding path.

## iTerm2 implementation order

1. **Primary candidate:** iTerm2 Run Coprocess binding that runs `sshpic paste --output=payload` and inserts stdout as user input.
2. **Secondary candidate:** iTerm2 Python API RPC keybinding if Coprocess cannot satisfy no-newline/no-recursion behavior.
3. **Last-resort candidate:** AppleScript only if it passes no-extra-newline, no-recursion, and focus-safety checks.

Important spike risk:

- iTerm2 sessions can have Coprocess limitations. If a session already has an active Coprocess, direct-paste spike must verify conflict behavior and either fall back to Python API RPC or document the limitation.

## CLI surface

Required commands:

```bash
sshpic init
sshpic paste
sshpic clip
sshpic shot
sshpic full
sshpic file <path...>
sshpic doctor
sshpic clean
sshpic version
sshpic snippet <terminal>
sshpic install <terminal>
```

Purpose:

- `sshpic paste`: direct-paste primitive for terminal integrations.
- `sshpic clip`: explicit one-shot clipboard image upload for diagnostics/scripts.
- `sshpic snippet iterm2`: print dotfiles-friendly iTerm2 integration snippet.
- `sshpic install iterm2`: optional guided local terminal integration.

## Config design

Config path:

```text
~/.config/sshpic/config.toml
```

Example:

```toml
remote_host = "codex141"
remote_dir = "/tmp/sshpic/${USER}"
copy_to_clipboard = true
filename_template = "sshpic-{timestamp}-{rand}.png"

[paste]
mode = "smart"
terminal = "iterm2"
shortcut = "cmd+v"
insert_newline = false
text_passthrough = true

[macos]
clipboard_tool = "pngpaste"
screenshot_tool = "screencapture"
text_clipboard_tool = "pbpaste"
copy_tool = "pbcopy"

[upload]
method = "ssh-cat"
verify_sha256 = true
```

Priority:

```text
CLI flag > environment variable > config file > default
```

Environment variables:

```bash
SSHPIC_REMOTE_HOST
SSHPIC_REMOTE_DIR
SSHPIC_COPY_TO_CLIPBOARD
SSHPIC_CONFIG
SSHPIC_PASTE_MODE
```

## Internal architecture

Provider-style boundaries:

```go
type LocalImageSource interface {
    ReadClipboardImage(ctx context.Context) (LocalImage, error)
    CaptureFullScreen(ctx context.Context) (LocalImage, error)
    CaptureRegion(ctx context.Context) (LocalImage, error)
    ReadClipboardText(ctx context.Context) (string, error)
    CopyTextToClipboard(ctx context.Context, text string) error
}

type RemoteUploader interface {
    Upload(ctx context.Context, localPath string, remotePath string) error
    Verify(ctx context.Context, localPath string, remotePath string) (VerifyResult, error)
    Clean(ctx context.Context, remoteDir string) error
}

type TerminalInserter interface {
    Snippet(ctx context.Context, terminal string) (Snippet, error)
    Install(ctx context.Context, terminal string) error
    Doctor(ctx context.Context) []Check
}
```

Initial packages:

```text
internal/provider/macos
internal/upload/sshcat
internal/paste
internal/terminal/iterm2
internal/doctor
internal/shellquote
internal/pathfmt
```

## Target repository structure

```text
sshpic/
  README.md
  LICENSE
  CHANGELOG.md
  CONTRIBUTING.md
  CODE_OF_CONDUCT.md
  SECURITY.md
  install.sh
  Brewfile.example
  go.mod
  cmd/sshpic/main.go
  internal/app/commands.go
  internal/config/config.go
  internal/config/config_test.go
  internal/provider/provider.go
  internal/provider/macos.go
  internal/provider/linux.go
  internal/provider/windows.go
  internal/upload/sshcat.go
  internal/upload/sshcat_test.go
  internal/paste/paste.go
  internal/paste/paste_test.go
  internal/terminal/iterm2/snippet.go
  internal/terminal/iterm2/doctor.go
  internal/doctor/doctor.go
  internal/pathfmt/pathfmt.go
  internal/pathfmt/pathfmt_test.go
  internal/shellquote/shellquote.go
  internal/shellquote/shellquote_test.go
  docs/getting-started.md
  docs/dotfiles.md
  docs/troubleshooting.md
  docs/platform-support.md
  docs/security.md
  docs/comparison.md
  docs/roadmap.md
  examples/config.toml
  examples/zshrc.example
  examples/bashrc.example
  .github/workflows/ci.yml
  .github/workflows/release.yml
  .github/workflows/scorecard.yml
  .github/ISSUE_TEMPLATE/bug_report.yml
  .github/ISSUE_TEMPLATE/feature_request.yml
  .github/ISSUE_TEMPLATE/platform_support.yml
  .github/pull_request_template.md
```

## Security requirements

- Never print secrets, SSH private key paths, tokens, or full environment dumps in debug output.
- Default remote file mode: `0600`.
- Remote command starts with `umask 077`.
- Shell-safe quote all remote dirs and paths.
- Direct-paste output is payload-only: no shell command, no control sequence, no accidental newline, no recursive paste trigger.
- Use timestamp + random suffix filenames.
- `sshpic clean` refuses dangerous paths: empty string, `/`, `/tmp`, `$HOME`, `~`, and any non-sshpic-specific directory unless explicit safe checks pass.
- Add `docs/security.md` and root `SECURITY.md`.

## Testing plan

### Unit tests

- Config priority: CLI flag > env var > config file > default.
- Filename generation: timestamp + random suffix + safe extension.
- Remote path construction stays under configured remote dir.
- Shell quoting handles spaces, quotes, `$`, backticks, and unicode.
- Upload command includes `umask 077` and chmod `600`.
- SHA256 mismatch is detected.
- Debug output redacts secrets and key paths.
- `sshpic paste --output=payload` emits only insertable payload.
- Text passthrough emits original text exactly once.
- Empty clipboard returns non-zero without stdout garbage.
- `sshpic clean` refuses dangerous paths.

### macOS + iTerm2 direct-paste tests

- Install/snippet creates shortcut invoking `sshpic paste --output=payload` without typing command text into shell history or terminal prompt.
- Image clipboard + shortcut inserts `/tmp/sshpic/<user>/sshpic-...png` at cursor.
- Text clipboard + shortcut inserts original text exactly once.
- No newline unless `insert_newline=true`.
- No recursion when Cmd+V smart paste is enabled.
- No debug text, shell commands, or terminal control sequences in inserted payload.
- Active iTerm2 Coprocess conflict is tested and either handled by Python API fallback or documented.

### SSH integration tests

- SSH host reachable.
- Host unreachable gives actionable error.
- Remote dir not writable gives actionable error.
- Remote file exists and `file -b` identifies PNG/JPEG.
- Remote SHA equals local SHA.

### Manual E2E checklist

1. Install `sshpic` locally.
2. Configure SSH host.
3. Run `sshpic doctor`.
4. Enable iTerm2 direct paste integration.
5. SSH to remote host.
6. Start Codex CLI or Claude Code inside SSH.
7. Take screenshot to clipboard.
8. Press `sshpic` paste shortcut.
9. Confirm terminal input receives remote path or verified agent attachment behavior.
10. Ask the agent to describe the image and verify it matches the screenshot.
11. Copy text and verify same shortcut still pastes text normally.
12. Run `sshpic clean --dry-run` and verify only sshpic files are listed.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| iTerm2 Coprocess inserts newline or cannot bind cleanly | Prototype first; fallback to iTerm2 Python API RPC; document shortcut behavior. |
| iTerm2 session already has an active Coprocess | Test conflict; fallback to Python API RPC or document limitation. |
| Cmd+V smart paste breaks normal text paste | Smart paste must pass text through exactly once; use a fallback shortcut only for debugging if Cmd+V conflicts. |
| README overclaims Codex/Claude behavior | Say “path insertion into terminal sessions”; do not imply guaranteed native image attachment unless verified. |
| Screenshot secrets leak to shared remote tmp | Use `0600`, `/tmp/sshpic/$USER`, security warning, and `clean`; no cloud. |
| Shell injection through remote path | Dedicated shellquote package with tests. |
| Cross-platform promise too broad | Mark Linux/Windows and non-iTerm2 terminals as roadmap/experimental until verified. |

## ADR

### Decision

Build `sshpic` as a Go CLI with a no-daemon direct-paste path powered by terminal keybindings. The primary v0.1 UX is copy/capture image → press Cmd+V → remote path inserted into active SSH agent input.

### Drivers

- User explicitly rejected command-based per-image workflows.
- Public OSS adoption requires simple install and clear failure modes.
- Security posture favors one-shot SSH upload over daemon/cloud/remote install.

### Alternatives considered

- One-shot CLI only: rejected as primary because it violates direct paste.
- Daemon clipboard bridge: rejected for v0.1 because it conflicts with no-daemon positioning and increases trust burden.
- iTerm2-only product identity: rejected because future Linux/Windows support is required.

### Why chosen

Terminal keybinding direct paste is the smallest architecture that satisfies “no visible command” while preserving no-daemon/no-remote-install/no-cloud principles.

### Consequences

- v0.1 must spend real effort on terminal integration, not just CLI upload.
- Default shortcut is Cmd+V smart paste.
- Text passthrough must preserve normal paste behavior exactly once.
- Cross-terminal support becomes a roadmap of snippets/providers.
- README release language must be strict: only macOS+iTerm2 is supported in v0.1; Codex/Claude support means path insertion into a terminal session, not guaranteed native image attachment.

## Recommended execution path

Default durable implementation path:

```text
$ultragoal + optional $team
```

Team launch hint after approval:

```bash
cd /Users/kyungmoon/sshpic
omx team 4:executor "Implement sshpic MVP from .omx/plans/ralplan-final-sshpic-oss-mvp.md with direct-paste as P0"
```

Suggested lanes:

1. `executor`: Go CLI/config/path/shellquote foundation.
2. `executor`: macOS provider + SSH upload.
3. `executor`: `sshpic paste` + iTerm2 snippet/install.
4. `test-engineer` or `verifier`: tests, manual QA, release/claim audit.

## Completion criteria for v0.1

- `go test ./...` passes.
- `go vet ./...` passes.
- macOS arm64/amd64 builds pass.
- Linux arm64/amd64 build is compile-only or clearly marked experimental.
- GoReleaser dry-run succeeds.
- `sshpic clip --debug` equivalent proves local/remote SHA match.
- iTerm2 direct-paste E2E is documented with evidence.
- README claim audit passes: no unverified Codex/Claude/Linux/Windows claims.
- `sshpic clean --dry-run` safety is proven.

## Ralplan consensus status

Planning gate completed:

- Architect review: **APPROVE**
- Critic review: **APPROVE**

Final planning artifacts were originally prepared under `/Users/kyungmoon/sshpic/.omx/plans/` and summarized here for repository handoff.
