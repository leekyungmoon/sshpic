# Dotfiles

Normal setup is installer-driven. The files remain plain text for people who manage dotfiles.

Config path:

```text
~/.config/sshpic/config.toml
```

iTerm2 Dynamic Profile path, when generated:

```text
~/Library/Application Support/iTerm2/DynamicProfiles/sshpic.json
```

Machine-specific overrides can come from environment variables:

```sh
export SSHPIC_REMOTE_HOST=codex141
export SSHPIC_REMOTE_DIR='/tmp/sshpic/${USER}'
```

Locked-down machines that cannot write iTerm2 preferences are outside the normal installer path; see troubleshooting for fallback design notes.
