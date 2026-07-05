# Roadmap

## v0.1

- macOS + iTerm2 direct path insertion.
- iTerm2 direct paste installed by default through Python RPC, with installer-managed runtime provisioning and safe-fail if provisioning is unavailable.
- Foreground SSH target detection at paste time.
- SSH stdin upload.
- Payload-only paste primitive.
- Safety-first cleanup.

## Later

- Harden local tmux SSH target detection after real tester evidence.
- Terminal.app and Ubuntu GNOME Terminal capability probes, restore hooks, and target-specific Codex E2E scripts before any direct-paste support claim.
- Planned/experimental Terminal.app, Warp, Ghostty, WezTerm, and Kitty integrations after native paste safety is proven per target.
- Linux screenshot and clipboard providers as provider work only, not a direct-paste support claim.
- Windows/WSL provider.
- Optional richer JSON diagnostics for scripted workflows.
