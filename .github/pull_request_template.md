## Summary

## Verification

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./cmd/sshpic`

## Claim audit

- [ ] Windows claims are limited to the documented Windows 10/11 + WezTerm + focused Windows-local `ssh.exe` surface; Linux, Windows Terminal, WSL, PuTTY, and other terminals remain unclaimed without target E2E evidence.
- [ ] No Terminal.app or Ubuntu direct-paste support claim without target-specific E2E evidence.
- [ ] No overclaiming Codex/Claude native image attachment behavior.
- [ ] Shortcut text paste still delegates native paste; no default payload/text retyping fallback.
- [ ] Shortcut target selection uses focused session/window evidence, not global SSH/config fallback.
- [ ] Payload-only behavior remains protected.
