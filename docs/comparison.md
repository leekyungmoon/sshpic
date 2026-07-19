# Comparison

## One-shot upload scripts

One-shot scripts can upload an image, but they usually require typing a command after every screenshot. `sshpic` puts that work behind the installed iTerm2 `Cmd+V` or WezTerm `Ctrl+V` path, so the normal flow is a paste gesture that inserts a remote path.

## Clipboard daemons

Clipboard daemons can watch every clipboard change automatically, but that adds broad lifecycle and trust surface. `sshpic` does not continuously watch the clipboard; supported terminal integrations run on demand when the installed paste shortcut is invoked.

## Cloud image uploads

Cloud uploaders are convenient but introduce third-party storage. `sshpic` uploads only to the active SSH host.
