# Development

## Run from source

On x86-64 Linux or in WSL, run `nix develop -c just build` from the repository root. This installs the locked frontend dependencies, builds the page in `web/dist/` and embeds it in `dist/vrc-tsuyaku`. Start it with `./dist/vrc-tsuyaku -data-dir ~/vrc-tsuyaku-data`.

Run `just web` inside the dev shell before using `go build`, `go test` or golangci-lint directly on a fresh checkout.

### Build without Nix on Linux or Windows

1. Install Go 1.26 or later and Node.js 24 with npm.
2. Build the frontend from the repository root.

   ```sh
   npm --prefix web ci --ignore-scripts
   npm --prefix web run build
   ```

3. Build the executable with cgo off as in the release builds.

   On Linux:

   ```sh
   CGO_ENABLED=0 go build ./cmd/vrc-tsuyaku
   ```

   On Windows, in PowerShell:

   ```powershell
   $env:CGO_ENABLED = "0"
   go build ./cmd/vrc-tsuyaku
   ```

4. Start it with a data folder outside the repository. Without `-data-dir`, `settings.yaml` and `downloads/` land in the repository, where git does not ignore them.

   On Linux:

   ```sh
   ./vrc-tsuyaku -data-dir ~/vrc-tsuyaku-data
   ```

   On Windows, in PowerShell:

   ```powershell
   .\vrc-tsuyaku.exe -data-dir $HOME\vrc-tsuyaku-data
   ```

## Checks

The Nix dev shell supports only x86-64 Linux, so run the checks there or in WSL on Windows:

```sh
nix develop -c just lint test
nix develop -c sh -c 'just web && GOOS=windows go build -o /dev/null ./...'
```

`just lint` fails if a JS or JSX file in `web/` does not match Prettier's format. `nix develop -c npm --prefix web run format` formats them.

CI also runs the tests with `-race`. [cmd/vrc-tsuyaku/README.md](../cmd/vrc-tsuyaku/README.md) covers the HTTP API and the end-to-end checks, which `just test` skips unless their environment variables are set. [AGENTS.md](../AGENTS.md) holds the repository's rules.

## Release

Pushing a `v*` tag publishes a release with no draft step. `.github/workflows/release.yml` runs `just release` with the tag, which builds `vrc-tsuyaku-windows.zip`, `vrc-tsuyaku-linux.tar.gz` and `SHA256SUMS` into `dist/`. The release notes are [install.md](install.md) and [install.ja.md](install.ja.md), followed by the notes that GitHub generates.

CI and the release workflow build with the Go version in the `toolchain` line of `go.mod`. Before a release, set that line to the latest Go patch release, so that the executables carry its security fixes. `govulncheck -mode=binary` on a built executable lists the Go vulnerabilities that it can reach.

`just release` stops in two cases:

- HEAD does not carry the tag: "Expected HEAD to be tagged v0.1.0."
- git reports changed or untracked files. Go then stamps the executables as `v0.1.0+dirty`, and the recipe stops with "… does not report release version v0.1.0."

1. Tag the latest commit on `main` and build the archives:

   ```sh
   git switch main
   git pull
   git tag v0.1.0
   nix develop -c just release v0.1.0
   ```

2. Copy `dist/vrc-tsuyaku-windows.zip` to a folder on Windows, extract it there, and start the app. Check that Settings → About shows v0.1.0, not "Development build".
3. Push the tag:

   ```sh
   git push origin v0.1.0
   ```

If step 2 finds a problem, delete the local tag with `git tag -d v0.1.0`, fix the problem on `main`, and start again from step 1.
