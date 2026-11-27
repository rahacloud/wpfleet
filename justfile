default:
    @just --list

# Build the wpfleet binary
build:
    go build -o wpfleet ./cmd/wpfleet

# Format and generate the templ components; commit what it writes
generate:
    go tool templ fmt internal/infra/web/ui
    go tool templ generate

# Run every test with the race detector
test:
    go test -race ./... -covermode=atomic -coverprofile=coverage.out

# Lint the Go code and the charts
lint:
    golangci-lint run -c .golangci.yml
    helm lint charts/wordpress -f charts/wordpress/ci/test-values.yaml
    helm lint deployments/helm/wpfleet -f deployments/helm/wpfleet/ci/test-values.yaml

# Build the server image (proxy variables reach go mod download, not the image);
# the version and commit come from Git, since the build context has no .git
image tag="wpfleet:dev":
    docker build -f build/package/Dockerfile \
        --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY \
        --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy \
        --build-arg VERSION="$(git describe --tags --always --dirty)" \
        --build-arg COMMIT="$(git rev-parse --short HEAD)" \
        -t {{tag}} .

tidy:
    go mod tidy

update:
    go get -u ./...
    go mod tidy

# Run the release into dist/ without publishing or signing (docs/releasing.md)
release-snapshot:
    goreleaser release --snapshot --clean --skip=publish,sign
