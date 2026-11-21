# Development

[design.md](design.md) maps the pipeline to its packages. [AGENTS.md](../AGENTS.md) holds the repository's rules.

## Build and run

On x86-64 Linux or in WSL, with Nix:

```sh
nix develop -c just build
./dist/vrc-tsuyaku -data-dir ~/vrc-tsuyaku-data
```

Without Nix, on Linux or Windows, install Go 1.26 or later and Node.js 24. Then build the page and the executable:

```sh
npm --prefix web ci --ignore-scripts
npm --prefix web run build
CGO_ENABLED=0 go build ./cmd/vrc-tsuyaku
```

In PowerShell, set `$env:CGO_ENABLED = "0"` before `go build`.

Start either build with `-data-dir`, as the first block does, to keep the settings and the downloads out of the checkout. Without it, the app writes them beside the executable.

## Checks

```sh
nix develop -c just lint test
nix develop -c sh -c 'just web && GOOS=windows go build -o /dev/null ./...'
```

A test that needs models or audio skips unless its `VRC_TSUYAKU_TEST_*` variables are set. The doc comment of each such test names its variables and its command.

## Release

Pushing a `v*` tag publishes a release with no draft step. The comment above the `release` recipe in the [justfile](../justfile) lists the steps before the push.
