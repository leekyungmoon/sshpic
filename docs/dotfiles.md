# Dotfiles

Normal setup is installer-driven. The files remain plain text for people who manage dotfiles.

Config path:

```text
~/.config/sshpic/config.toml
```

The same path is used relative to the Windows user profile (for example, `%USERPROFILE%\.config\sshpic\config.toml`).

When Python RPC is available, sshpic uses this iTerm2 AutoLaunch script path:

```text
~/.config/iterm2/AppSupport/Scripts/AutoLaunch/sshpic_smart_paste.py
```

Machine-specific overrides can come from environment variables when you explicitly want them:

```sh
export SSHPIC_REMOTE_HOST=codex141
export SSHPIC_REMOTE_DIR='/home/${USER}/.sshpic/images'
```

The normal iTerm2 paste path detects the foreground `ssh` target at paste time, so dotfiles do not need to pin a host for everyday use.

The Windows WezTerm path also does not need or allow a configured-host fallback for shortcut-driven target selection. It uses only the focused pane's native `ssh.exe` foreground-process `argv`.

Do not copy an installer-generated WezTerm backup between machines or hand-edit sshpic ownership markers as shared dotfiles. Use the lifecycle commands on each Windows machine so user configuration is backed up and restored locally:

```text
sshpic install wezterm
sshpic doctor wezterm
sshpic restore wezterm
```
