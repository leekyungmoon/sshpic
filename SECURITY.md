# Security Policy

## Supported versions

`sshpic` v0.1 is pre-1.0 software. Security fixes target the latest release.

## Reporting a vulnerability

Please report vulnerabilities privately by opening a GitHub security advisory or emailing the maintainer listed on the repository profile. Do not include sensitive screenshots or private keys in public issues.

## Security boundaries

`sshpic` performs local capture/clipboard reads and SSH uploads to a host you configure. It does not run a daemon by default, install remote software, upload to cloud storage, or mutate SSH config by default.

Remote files are written under the configured `remote_dir` with `umask 077` and `chmod 600`. You are responsible for choosing a remote host and directory appropriate for sensitive screenshots.
