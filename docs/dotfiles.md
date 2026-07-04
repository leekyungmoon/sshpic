# Dotfiles

Normal setup is installer-driven. The files remain plain text for people who manage dotfiles.

Config path:

```text
~/.config/sshpic/config.toml
```

iTerm2 Python API AutoLaunch script path:

```text
~/.config/iterm2/AppSupport/Scripts/AutoLaunch/sshpic_smart_paste.py
```

Machine-specific overrides can come from environment variables when you explicitly want them:

```sh
export SSHPIC_REMOTE_HOST=codex141
export SSHPIC_REMOTE_DIR='/home/${USER}/.sshpic/images'
```

The normal iTerm2 paste path detects the foreground `ssh` target at paste time, so dotfiles do not need to pin a host for everyday use.
