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

Install the local iTerm2 Cmd+V smart-paste hook with:

```sh
sshpic install iterm2
```

Print reproducible iTerm2 setup text with:

```sh
sshpic snippet iterm2
```

Keep `~/.config/sshpic/config.toml` in dotfiles; do not commit local iTerm2 plist files.
