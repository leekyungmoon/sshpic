# Contributing

Thanks for helping with `sshpic`.

## Development loop

```sh
go test ./...
go vet ./...
go build ./cmd/sshpic
```

## Pull requests

- Keep v0.1 support claims strict: macOS + iTerm2 direct paste only.
- Do not claim native Codex/Claude image attachment behavior without evidence.
- Do not add daemons, cloud uploads, remote installs, or SSH config mutation as defaults.
- Add tests for shell quoting, clean safety, and payload-only output when touching upload or paste code.
