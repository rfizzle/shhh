The declaration is `.shhh/toolchain.toml`, and it takes four keys:

```toml
packages = ["shellcheck"]
install = [
  "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0",
  "go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4",
]
hosts = ["proxy.golang.org", "sum.golang.org"]
check = ["golangci-lint", "gosec", "shellcheck"]
```

- `packages` are names in the base image's own package index — Wolfi, so
  apk's — alone or as `name=version`.
- `install` are command lines, one install each, every package on them at one
  exact version.
- `hosts` are the registries an install run on the host may reach — host names
  alone, with no scheme, port, path or wildcard.
- `check` are the binaries the work expects to find on `PATH`, named as a
  program is named on `PATH` rather than as a path.

The file is read whole or not at all. A key it does not know, a host that is
not a host name, a check that is a path rather than a program, or an install
line whose pin cannot be read is refused when the file is read, and the
refusal quotes the entry.

**An install line names one exact version, or it is refused.** A place to
work prepared from the declaration is kept under the file's bytes, so it is
prepared once and not on every run — and that is only true while the same
bytes mean the same tools. `@latest`, `@master`, `@v1`, a bare `apk add jq`,
`pip install ruff` with no `==`, `npm i -g prettier` with no `@version`, or
anything that installs from a requirements file, a branch or a working tree,
means something different next month under an unchanged line, and a cache
keyed on the line would go on serving last month's tool. So a line is
accepted only where the pin can be read: one command, with no chain, pipe,
redirection, substitution or quote, from an installer whose arguments say
the version. Each installer states its pin one way:

| Installer | A pinned line |
|---|---|
| `go install` | `go install example.com/cmd/tool@v1.2.3` — a full `vX.Y.Z` or a commit hash of twelve or more characters |
| `pip install`, `pip3 install`, `python -m pip install`, `python3 -m pip install`, `pipx install` | `pip install package==1.2.3` — `==` and nothing else; no `-r`, `-c`, `-e`, `-U` or `--pre` |
| `npm install`, `npm i`, `npm add`, `pnpm add` | `npm install package@1.2.3` — one exact version; no range and no `--tag` |
| `cargo install` | `cargo install crate@1.2.3`, or `--version 1.2.3` for the one crate on the line; no `--git`, `--branch` or `--path` |
| `apk add` | `apk add package=1.2.3-r0` — for the image, never run on this machine; no `-u` |

A line may begin with `NAME=value` assignments, which are its environment.
Any other installer is refused rather than guessed at, since a pin that
cannot be read is one nobody can vouch for.

What the lines install lands in shhh's own directory — `shhh/toolchain/bin`
under the user cache directory, never `~/go/bin` or a global prefix — and
that directory is on the end of the `PATH` every command of a session is
handed, which is where the `check` names are looked for.
