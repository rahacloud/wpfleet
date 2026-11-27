default:
    @just --list

# Build the wpfleet binary
build:
    go build -o wpfleet ./cmd/wpfleet

# Run every test with the race detector
test:
    go test -race ./... -covermode=atomic -coverprofile=coverage.out

# Lint the Go code and the chart
lint:
    golangci-lint run -c .golangci.yml
    helm lint charts/wordpress -f charts/wordpress/ci/test-values.yaml

tidy:
    go mod tidy

update:
    go get -u ./...
    go mod tidy

# Run the release into dist/ without publishing or signing (docs/releasing.md)
release-snapshot:
    goreleaser release --snapshot --clean --skip=publish,sign
