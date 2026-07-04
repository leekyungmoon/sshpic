# Roadmap

## v0.1

- macOS + iTerm2 direct path insertion.
- iTerm2 direct paste installed by default only when the Python RPC runtime is ready; runtime-missing Macs fail safely until a non-polluting architecture is proven.
- Foreground SSH target detection at paste time.
- SSH stdin upload.
- Payload-only paste primitive.
- Safety-first cleanup.

## Later

- Harden local tmux SSH target detection after real tester evidence.
- Verified Terminal.app, Warp, Ghostty, WezTerm, and Kitty integrations.
- Linux screenshot and clipboard providers.
- Windows/WSL provider.
- Optional richer JSON diagnostics for scripted workflows.
