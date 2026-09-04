# shhh

[![CI](https://github.com/rfizzle/shhh/actions/workflows/test.yml/badge.svg)](https://github.com/rfizzle/shhh/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/rfizzle/shhh?display_name=tag&sort=semver)](https://github.com/rfizzle/shhh/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/rfizzle/shhh.svg)](https://pkg.go.dev/github.com/rfizzle/shhh)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Natural language to shell commands, conversations, and supervised coding work —
without leaving the terminal.

```text
$ shhh cmd find all go files changed in the last week
$ find . -name "*.go" -mtime -7
  lists Go files under the current directory modified in the last seven days.
  ⛨ read-only · no network · no sudo

[↵] run  [e] edit  [r] revise  [x] explain  [c] copy  [s] save  [esc] quit
```

## Install

### Homebrew

```sh
brew install rfizzle/tap/shhh
```

### Go

```sh
go install github.com/rfizzle/shhh/cmd/shhh@latest
```

### Windows

Download the release archive for your architecture from
[GitHub Releases](https://github.com/rfizzle/shhh/releases), put `shhh.exe` on
`PATH`, or install with Go. PowerShell is used when available and `cmd`
otherwise.

### From source

```sh
git clone https://github.com/rfizzle/shhh.git
cd shhh
make build
```

## Quick start

Set an API key, then ask for a command:

```sh
export SHHH_API_KEY="sk-..."
shhh cmd "list open ports on this machine"
```

Start a session when the task needs more context:

```sh
shhh chat
shhh code "fix the failing tests"
```

## Modes

| Command | Use it for |
|---|---|
| `shhh cmd <prompt>` | Generate one shell command and review it before running it. |
| `shhh chat [prompt]` | Explore questions with a read-only assistant and conversation tools. |
| `shhh code [prompt]` | Have a supervised agent read, edit, run, and verify work. |
| `shhh init <shell>` | Add inline command generation to Bash, Zsh, or Fish. |

Use `shhh cmd --raw` or pipe a prompt to `shhh cmd` when a script needs only the
command on standard output:

```sh
echo "list all docker containers" | shhh cmd
shhh cmd --raw "find large files" | sh
```

### Shell integration

Add inline command generation to your shell with Ctrl+K:

```sh
# zsh
eval "$(shhh init zsh)"

# bash
eval "$(shhh init bash)"

# fish
shhh init fish | source
```

## Safety by default

shhh shows generated commands before it runs them. Coding work is permissioned:
file edits and commands require approval unless you choose a more permissive
session mode. It reports known risk and containment limits rather than claiming
a command is safe when it cannot determine that.

On Linux and macOS, assistant-run commands can use OS-level containment. Windows
has no equivalent host mechanism; use `shhh code --sandbox` with Docker or
Podman when containment is needed.

Read about [approvals and safety](docs/capabilities/approvals-and-safety.md) and
[command containment](docs/capabilities/containment.md).

## Configuration

Set `SHHH_API_KEY`, as above, or open the interactive configuration editor:

```sh
shhh config
```

To read a setting without opening the editor, `shhh config list` prints every
key with the value in force and where it came from — a default, the file, or
an environment variable that outranks it — and `shhh config get <key>` prints
one. Both take `--json`.

Configuration is stored in `$XDG_CONFIG_HOME/shhh/config.toml`, or
`~/.config/shhh/config.toml` by default. shhh supports OpenAI, Anthropic,
Gemini, OpenRouter, OpenAI-compatible endpoints, and configurable gateway
profiles.

- [Configuration guide](docs/capabilities/configuration.md)
- [Providers and gateway profiles](docs/capabilities/providers.md)
- Run `shhh doctor` to check local setup.

## More capabilities

- **Project context:** `shhh init --project` creates `.shhh/project.md` for
  project-specific instructions.
- **Skills:** load reusable task guidance from project or user skill directories.
- **MCP:** connect tools supplied by Model Context Protocol servers.
- **Hooks:** run your own command before or after a tool, when a turn closes
  and when a session starts — a formatter after every edit, a refusal on a
  path, a notification when a long run stops. Entries live in the `[hooks]`
  table or in a trusted checkout's `.shhh/hooks.json`; a hook can refuse a
  call or rewrite its arguments and can never turn a read into a write.
- **Secrets:** make environment values available to commands without exposing
  their values to the model.
- **Sub-agents:** delegate scoped research or isolated implementation work.
- **Quality gates:** run trusted repository checks and retain their results.
  Suites live in `.shhh/quality.json`; an `"on_close"` key there names the one
  a turn runs as it closes over files it changed, which `shhh code -p` and the
  backlog runner honour by default and `/gate on` switches on in a session.
- **Context window:** `/context` shows where the window went and how much of
  the last request came back from the provider's cache. Past 80% full a
  session elides its oldest tool results down to 60%, and `/compact` replaces
  the conversation with a summary when that is no longer enough.
- **Sessions and memory:** resume conversations, retain local history, and save
  durable preferences or project conventions.

Browse the [capability documentation](docs/capabilities/README.md) for the
complete guide. The [product overview](docs/product.md) explains the intended
use, and [AGENTS.md](AGENTS.md) describes the repository for contributors and
coding agents.

## Development

```sh
make build
go test ./...
make lint
make ci
```

## License

[MIT](LICENSE)
