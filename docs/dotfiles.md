# Dotfiles

`sshpic` is configured by a plain TOML file at:

```text
~/.config/sshpic/config.toml
```

You can keep that file in your dotfiles and use environment variables for machine-specific values:

```sh
export SSHPIC_REMOTE_HOST=codex141
export SSHPIC_REMOTE_DIR='/tmp/sshpic/${USER}'
```

Print reproducible iTerm2 setup text with:

```sh
sshpic snippet iterm2
```

The snippet is intentionally text-first so you can document the key mapping without committing local iTerm2 profile plist files.
