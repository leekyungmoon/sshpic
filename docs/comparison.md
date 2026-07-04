# Comparison

## One-shot upload scripts

One-shot scripts can upload an image, but they usually require typing a command after every screenshot. `sshpic` puts that command behind the generated iTerm2 profile-local `Cmd+V` path, so the normal flow is a paste gesture that inserts a remote path.

## Clipboard daemons

Daemons can watch clipboard changes automatically, but v0.1 avoids the extra trust and lifecycle burden. `sshpic` is invoked on demand by the terminal integration.

## Cloud image uploads

Cloud uploaders are convenient but introduce third-party storage. `sshpic` uploads only to the selected SSH host.
