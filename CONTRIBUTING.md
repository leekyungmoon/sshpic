# Contributing

Thanks for helping with `sshpic`.

## Development loop

```sh
go test ./...
go vet ./...
go build ./cmd/sshpic
```

External, opt-in checks before tagged support claims:

```sh
scripts/verify-iterm2-e2e.sh
SSHPIC_INTEGRATION_HOST=<host> SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" scripts/verify-ssh-integration.sh
```

## Pull requests

- Keep v0.1 support claims strict: macOS + iTerm2 direct paste only.
- Do not claim native Codex/Claude image attachment behavior without evidence.
- Do not add daemons, cloud uploads, remote installs, or SSH config mutation as defaults.
- Add tests for shell quoting, clean safety, and payload-only output when touching upload or paste code.
