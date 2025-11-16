# incogniterm

Disposable fake-identity terminal for demos and recordings.  
It runs your shell in a PTY with a random user, hostname, and ephemeral `$HOME`.

## Features

- Random fake username and hostname per session  
- Ephemeral `$HOME` with temporary shell config and history  
- Wrapper `id`, `whoami`, and `hostname` reporting the fake identity  
- Automatic cleanup of the temporary environment on exit  

> Note: incogniterm is **not** a security boundary. It only hides identity and configuration for demos; filesystem and permissions remain unchanged.

## Installation

With Go 1.25+:

```bash
go install github.com/eugeniofciuvasile/incogniterm@latest
```

Ensure your `GOPATH/bin` or `GOBIN` is on your `PATH`.

## Usage

```bash
incogniterm
```

You will be dropped into an incognito shell session.  
Use it to record or demonstrate commands without exposing your real user, host, or shell setup.

Exit the shell as usual (`exit`, `Ctrl-D`) to clean up the temporary environment.
