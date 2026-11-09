# Build, test, and development commands. Run them inside `nix develop`.

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
    go run ./internal/ortsetup -data-dir .local/onnxruntime
