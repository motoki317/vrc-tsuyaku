# Run these recipes inside `nix develop` on x86-64 Linux or in WSL on Windows.
# Release packaging follows motoki317/noctiluca's workflow (MIT):
# https://github.com/motoki317/noctiluca/blob/d072f072d3ba748f6fb1c7d0f10739ae24c1b16e/.github/workflows/release.yml

[private]
default:
    @just --list

# On a fresh checkout, run `just web` before calling `go build`, `go test` or
# golangci-lint directly because the Go package embeds `web/dist/`.
# Build the static files embedded in the executable.
[working-directory: 'web']
web:
    if [ ! -f node_modules/.package-lock.json ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then npm ci --ignore-scripts; fi
    npm run build

# Run the unit tests. Tests that need models or audio skip unless their environment variables are set.
test: web
    npm --prefix web test
    go test -shuffle=on ./...

# Run `npm --prefix web run format` to fix Prettier errors in the web source.
# Run the linters and formatters in check mode.
lint: web
    npm --prefix web run format:check
    golangci-lint run

# Run `just build` after editing `web/src/`, `web/i18n/` or `web/icons.svg`.
# Build every command for this OS into dist/.
build: web
    mkdir -p dist
    go build -o dist/ ./...

# Copy licenses for other code into cmd/vrc-tsuyaku/notices/other/ by hand and record each origin in SOURCE.
# Refresh the license texts embedded in the executable after a dependency change.
notices:
    go run ./internal/notices

# Before a release, set the `toolchain` line in `go.mod` to the latest Go patch release for its security fixes.
# Run `govulncheck -mode=binary` on a built executable to list the Go vulnerabilities it can reach.
#
# Switch to `main` and pull its latest commit. Tag that commit with `git tag <tag>`.
# Run `just release <tag>` before pushing. Changes to tracked files or untracked files make Go
# append `+dirty` to the version, which fails the recipe's version check.
# On Windows, a person must copy and extract `dist/vrc-tsuyaku-windows.zip`, start the app
# and verify that Settings → About shows the tag in place of "Development build".
# If the check fails, delete the local tag with `git tag -d <tag>`, fix the problem on
# `main` and repeat from tagging. If it passes, run `git push origin <tag>`.
#
# Use the same release artifacts in local checks and CI.
release tag: web
    #!/usr/bin/env bash
    set -euo pipefail
    tag={{quote(tag)}}
    tag_commit=$(git rev-parse --verify "refs/tags/$tag^{commit}")
    if [[ "$tag_commit" != "$(git rev-parse HEAD)" ]]; then
        printf 'Expected HEAD to be tagged %s.\n' "$tag" >&2
        exit 1
    fi
    export CGO_ENABLED=0 GOFLAGS='' GOWORK=off
    mkdir -p dist
    stage=$(mktemp -d dist/.release.XXXXXX)
    trap 'rm -r -- "$stage"' EXIT
    mkdir -p "$stage/windows" "$stage/linux/vrc-tsuyaku"
    GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=true -o "$stage/windows/vrc-tsuyaku.exe" ./cmd/vrc-tsuyaku
    linux_interpreter=/lib64/ld-linux-x86-64.so.2
    GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=true -ldflags="-I=$linux_interpreter" -o "$stage/linux/vrc-tsuyaku/vrc-tsuyaku" ./cmd/vrc-tsuyaku
    actual_interpreter=$(LC_ALL=C readelf --program-headers "$stage/linux/vrc-tsuyaku/vrc-tsuyaku" | awk '$1 == "[Requesting" && $2 == "program" && $3 == "interpreter:" { sub(/\]$/, "", $4); print $4 }')
    if [[ "$actual_interpreter" != "$linux_interpreter" ]]; then
        printf 'Expected Linux ELF interpreter %s, got %s.\n' "$linux_interpreter" "$actual_interpreter" >&2
        exit 1
    fi
    for executable in "$stage/windows/vrc-tsuyaku.exe" "$stage/linux/vrc-tsuyaku/vrc-tsuyaku"; do
        if ! go version -m "$executable" | awk -v tag="$tag" '$1 == "mod" && $3 == tag { found = 1 } END { exit !found }'; then
            printf '%s does not report release version %s.\n' "$executable" "$tag" >&2
            go version -m "$executable" >&2
            exit 1
        fi
    done
    zip -j "$stage/vrc-tsuyaku-windows.zip" "$stage/windows/vrc-tsuyaku.exe"
    tar -C "$stage/linux" -czf "$stage/vrc-tsuyaku-linux.tar.gz" vrc-tsuyaku/vrc-tsuyaku
    (cd "$stage" && sha256sum vrc-tsuyaku-windows.zip vrc-tsuyaku-linux.tar.gz > SHA256SUMS)
    { cat docs/install.md; printf '\n'; cat docs/install.ja.md; } > "$stage/release-notes.md"
    mv -- "$stage/vrc-tsuyaku-windows.zip" "$stage/vrc-tsuyaku-linux.tar.gz" "$stage/SHA256SUMS" "$stage/release-notes.md" dist/

# Download the pinned ONNX Runtime CPU libraries for Linux and Windows into .local/onnxruntime/.
ort:
    go run ./internal/ortsetup -data-dir .local/onnxruntime
