# Security Policy

## Supported versions

Security fixes target the latest `main` branch until tagged releases begin.

## Reporting a vulnerability

Please open a private security advisory on GitHub or contact the maintainer directly. Include reproduction steps, affected commit, platform, and whether a real SSH host was involved.

## Security model

`sshpic` performs local capture/clipboard reads and SSH uploads to the active SSH target or to an explicit host you configure. It does not install remote software, upload to cloud storage, or mutate SSH config by default.

The iTerm2 integration installs a Python API AutoLaunch script so `Cmd+V` can run the payload path without Coprocess stderr popups. It does not continuously watch clipboard changes; it acts when the installed paste shortcut is invoked.

Remote writes use `umask 077`, quoted paths, and mode `0600`. `sshpic clean` refuses broad or dangerous directories.
