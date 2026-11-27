# syntax=docker/dockerfile:1
# The wpfleet image: the static binary on a distroless base, as a non-root
# user. Build it from the repository root: just image.

ARG GO_VERSION=1.27

FROM docker.io/library/golang:${GO_VERSION}-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# The context has no .git (a worktree's .git is a file pointing outside it),
# so Go records no commit; the version and the commit come in as build
# arguments instead (just image passes them) and are stamped into wpfleet's
# version package, which prefers them. Without them --version says devel.
ARG VERSION=""
ARG COMMIT=""
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X github.com/rahacloud/wpfleet/internal/infra/version.version=${VERSION} -X github.com/rahacloud/wpfleet/internal/infra/version.commit=${COMMIT}" \
      -o /out/wpfleet ./cmd/wpfleet

# Static, with CA certificates and time zones, no shell; the nonroot user is
# 65532, which the chart's securityContext names.
# The same digest as Dockerfile.release; dependabot moves both.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
COPY --from=build /out/wpfleet /usr/local/bin/wpfleet
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/wpfleet"]
CMD ["serve"]
