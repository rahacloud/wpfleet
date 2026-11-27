# Contributing to wpfleet

Thank you for helping. This file says how to set up the code, what the code expects of a change, and how to send it. Everyone who takes part follows the [code of conduct](CODE_OF_CONDUCT.md). Report a vulnerability privately, as [SECURITY.md](SECURITY.md) says, never in an issue.

## Before you start

For a bug, open an issue with the bug report form first, unless the fix is small and obvious. For a new command, a new flag or a change to how a command behaves, open a feature request and agree on the design before you write the code: [docs/design.md](docs/design.md) is the contract that the packages are built against, and a change to it is easier to discuss before it is code.

## Set up

You need:

- Go, at the version in `go.mod`.
- [just](https://github.com/casey/just), which runs the recipes in the `justfile`.
- [golangci-lint](https://golangci-lint.run) v2 and [Helm](https://helm.sh) v3 or later, for `just lint`.
- PHP 8 on the `PATH`, for the tests of the one-time login plugin and the host fix-up. Without PHP those tests skip, so CI still runs them.

Then:

```bash
just build    # builds ./wpfleet
just test     # every test, with the race detector and coverage
just lint     # golangci-lint and helm lint of the embedded chart
```

Run `just` alone to list every recipe. Run `just lint` and `just test` before you open a pull request; CI runs the same checks, and `go mod tidy` must leave `go.mod` and `go.sum` unchanged.

To try a command against a real cluster, a local [kind](https://kind.sigs.k8s.io) cluster is enough for `sites`, `deploy`, `diff`, `promote` and `admin`. Use an invented fleet with sites like `blog` and `shop` on `example.com`.

## How the code is laid out

wpfleet follows the [standard Go project layout](https://github.com/golang-standards/project-layout):

- `cmd/wpfleet/main.go` only starts the program.
- `internal/cmd` holds the commands, built with [urfave/cli v3](https://cli.urfave.org).
- `internal/ops` holds the operations that the CLI and the server share.
- `internal/infra` talks to the world: Kubernetes, Helm, Git, S3, Docker.
- `internal/domain` holds the types and the rules, with no I/O.
- `charts/wordpress` is the site chart, embedded in the binary.

`internal/domain` imports nothing else from this module. `internal/infra` may import domain, `internal/ops` may import domain and infra, and `internal/cmd` imports ops. [docs/design.md](docs/design.md) has the full contract.

## What a change needs

- **Change `docs/design.md` in the same commit** when a command, a flag, a configuration key or a package's API changes.
- **Write tests.** Packages are tested against fakes (client-go's fake clientset, Helm's in-memory storage, a fake S3), so `just test` needs no cluster.
- **Keep golangci-lint green.** The configuration turns on nearly every linter (`default: all`); fix the finding rather than adding a `//nolint`, and when a `//nolint` is right, give the reason on the same line.
- **Use the libraries the code already uses:** urfave/cli v3 for commands, koanf v2 for configuration, `log/slog` for logs. Use real types and generics where a type fits, not `any`.
- **A chart change is a wpfleet change.** Keep `helm lint` green and document every value in `charts/wordpress/values.yaml`.
- **Never print a secret.** Commands never print database passwords, salts or S3 keys from site files, in output, logs or errors.
- **No real data.** Never commit a real organisation's name, host, address, registry, credential or data, in code, tests, fixtures, docs or commit messages. Use `example.com`, `registry.example.com` and sites like `blog` and `shop`.

## Commit messages

The subject line starts with a [Conventional Commits](https://www.conventionalcommits.org) prefix and says, in the imperative, what the change does:

```text
fix: keep the site's salts when a deploy finds no release
```

Use `feat`, `fix`, `docs`, `test`, `refactor`, `build`, `ci` or `chore`, with a scope in brackets when it helps (`build(deps):`). The release notes leave out `docs`, `test`, `ci` and `chore` commits, so pick the prefix that matches what a user of wpfleet would notice.

The body explains why, in prose paragraphs: the problem, the reason for this solution, and what you found on the way, including what reflects badly on the change. The diff already shows what changed.

A [Developer Certificate of Origin](https://developercertificate.org) sign-off is not required. By sending a contribution you agree that it is licensed under the [Apache License 2.0](LICENSE), as section 5 of the license says.

## Pull requests

Keep a pull request to one subject. Fill in the template: what the change does, why, and how you tested it. A maintainer reviews it; CI must be green before it merges.

## Releases

A maintainer releases by pushing a `v*` tag. GoReleaser builds the binaries, the archives and the checksums, and writes the release notes from the commit subjects, which is why the prefix matters. When release notes are generated on GitHub instead, `.github/release.yml` groups the pull requests by their labels.
