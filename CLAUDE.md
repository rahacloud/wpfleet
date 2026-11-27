# CLAUDE.md

wpfleet is RahaCloud's open-source CLI for running a fleet of WordPress sites on Kubernetes. `docs/design.md` is the contract between the packages: read it before changing a command or a package, and change it in the same commit as the code.

- **Go conventions come from the `go-projects` skill**: golang-standards/project-layout, urfave/cli v3, koanf v2, log/slog, golangci-lint with `default: all`, no `any` where a type fits, the latest Go. Before using a library (client-go, the Helm SDK, minio-go, urfave/cli), look up its current documentation with context7.
- **The repository will be public.** Never commit a client's name, host, address, registry, credential or data, in code, tests, fixtures, docs or commit messages. Use `example.com`, `blog`, `shop` and the like. The tool was first used for a real client migration; that client's configuration lives in its own private repository, not here.
- **The chart in `charts/wordpress` is embedded in the binary** (`charts.go`). A chart change is a wpfleet change: keep `helm lint` green and keep the values documented in `values.yaml`.
- **`deployments/helm/wpfleet` runs `wpfleet serve`** and `build/package/Dockerfile` builds its image. Keep `helm lint deployments/helm/wpfleet -f deployments/helm/wpfleet/ci/test-values.yaml` green, keep the Role's rules matching "Running it" in `docs/design.md`, and keep `just image` building.
- **The web UI's templ components are generated.** Edit the `.templ` files in `internal/infra/web/ui`, run `just generate`, and commit both; CI fails when the generated Go does not match. Every visible text goes through the catalogue in `ui/i18n`, in English and Persian.
- **Commands never print secrets** from site files (database passwords, salts, S3 keys).
- Work goes on `main`. Commit messages explain why, in prose.
