# wpfleet

Manage a fleet of WordPress sites on Kubernetes from one Git repository: deploy every site with one shared Helm chart, build and promote site images from development to production, open a site's admin with a one-time link, and back sites up to S3 and restore them.

> **Status: before its first release.** Every command in [docs/design.md](docs/design.md) is built and covered by tests against fakes, but none has run against a real cluster, registry or bucket yet; expect rough edges until v0.1. `docs/design.md` is the contract the commands are built against.

## How it works

A fleet is a Git repository with a `wpfleet.yaml` and one values directory per environment. Each site is one Helm release of the embedded [WordPress chart](charts/wordpress), rendered from the environment's `_shared.yaml` and the site's own file. The site's code (core, themes and plugins) is an immutable image; its uploads live on a volume and its data in MariaDB.

```bash
wpfleet sites list                          # every site, its host, image and readiness
wpfleet build blog --source ./blog -e development --set
wpfleet diff blog -e development           # what a deploy would change
wpfleet deploy blog -e development
wpfleet promote blog --to staging && wpfleet deploy blog -e staging
wpfleet admin blog -e staging --open         # one-time login link, valid 60 seconds
wpfleet backup run blog -e production
wpfleet backup restore blog -e staging --stamp latest --uploads
wpfleet copy blog --from production --to staging
```

## Development

```bash
just build    # ./wpfleet
just test
just lint     # golangci-lint and helm lint
```

## License

Apache-2.0, see [LICENSE](LICENSE).
