# Build, test, and development commands. Run them inside `nix develop`.

ort_version := "1.31.1"

[private]
default:
    @just --list

# Run the unit tests. Tests that need models or audio skip unless their environment variables are set.
test:
    go test -shuffle=on ./...

# Run the linters and formatters in check mode.
lint:
    golangci-lint run

# Build every command for this OS into dist/.
build:
    mkdir -p dist
    go build -o dist/ ./...

# Download the pinned ONNX Runtime CPU libraries for Linux and Windows into .local/onnxruntime/.
ort:
    #!/usr/bin/env bash
    set -euo pipefail
    dir=.local/onnxruntime
    mkdir -p "$dir"
    cd "$dir"
    base=https://github.com/microsoft/onnxruntime/releases/download/v{{ort_version}}
    sums='c849e36a1ef3e22d4ff217b9d1ce4a3cf5b9467b975610913ae959ad5b16bdc9  onnxruntime-linux-x64-{{ort_version}}.tgz
    5869e26bb25314375f2d5ef1877e3ea06b1543d6c710e6e4a1ec2f7177ab4b2c  onnxruntime-win-x64-{{ort_version}}.zip'
    while read -r sum name; do
      if ! echo "$sum  $name" | sha256sum -c --status 2>/dev/null; then
        curl -fL --retry 3 -o "$name.part" "$base/$name"
        echo "$sum  $name.part" | sha256sum -c --quiet
        mv "$name.part" "$name"
      fi
    done <<< "$sums"
    test -d onnxruntime-linux-x64-{{ort_version}} || tar xzf onnxruntime-linux-x64-{{ort_version}}.tgz
    test -d onnxruntime-win-x64-{{ort_version}} || unzip -q onnxruntime-win-x64-{{ort_version}}.zip
    echo "Linux: $PWD/onnxruntime-linux-x64-{{ort_version}}/lib/libonnxruntime.so.1"
    echo "Windows: $PWD/onnxruntime-win-x64-{{ort_version}}/lib/onnxruntime.dll"
