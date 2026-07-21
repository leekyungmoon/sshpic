# Roadmap

## v0.1

- macOS + iTerm2 direct path insertion.
- iTerm2 direct paste installed by default through Python RPC, with installer-managed runtime provisioning and safe-fail if provisioning is unavailable.
- Windows 10/11 + Windows Terminal 1.24.10921+ password-SSH image paste through sshpic's post-authentication Plink stdin proxy: an empty bracketed-paste image signal becomes a SHA-verified remote path, while non-empty text is forwarded byte-for-byte and Plink retains direct ownership of password input.
- Windows 10/11 + WezTerm password-SSH image paste through focused-pane dispatch and PuTTY 0.84 connection sharing, launched as normal `ssh user@host` from the same managed PowerShell 7 profile and rendered by Codex as `[Image #1]`; Windows PowerShell 5.1 is unsupported for that mapping, and native `ssh.exe` remains the key/agent path.
- One cross-platform `install.sh` implementation, run as the same literal `./install.sh` from Git Bash or PowerShell (with an explicit Git Bash form retained for automation that needs an exit code), with optional `winget` provisioning for Go, WezTerm, and PuTTY plus verified lifecycle coverage for both Windows terminal routes, the PowerShell mapping, and owned PuTTY sessions.
- Foreground SSH target detection at paste time.
- SSH stdin upload.
- Payload-only paste primitive.
- Safety-first cleanup.

## Later

- Harden local tmux SSH target detection after real tester evidence.
- Terminal.app and Ubuntu GNOME Terminal capability probes, restore hooks, and target-specific Codex E2E scripts before any direct-paste support claim.
- Planned/experimental Terminal.app, Warp, Ghostty, and Kitty integrations after native paste safety is proven per target.
- Linux screenshot and clipboard providers as provider work only, not a direct-paste support claim.
- WSL clipboard and terminal integration, tracked separately from native Windows WezTerm support.
- Optional richer JSON diagnostics for scripted workflows.
