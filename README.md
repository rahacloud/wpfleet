# wpfleet

Manage a fleet of WordPress sites on Kubernetes from one Git repository: deploy every site with one shared Helm chart, build and promote site images from development to production, open a site's admin with a one-time link, and back sites up to S3 and restore them.

> **Status: before its first release.** Every command in [docs/design.md](docs/design.md) is built and covered by tests against fakes. `sites list`, `deploy`, `diff`, `promote` and `admin` have run against a real cluster (a local kind cluster, 2026-11-27); `build`, `backup` and `copy` have not yet run against a real registry or bucket. Expect rough edges until v0.1. `docs/design.md` is the contract the commands are built against.

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

![wpfleet listing a fleet, promoting a site from development to staging, deploying it and making a login link](docs/images/demo.gif)

## Open a site's admin without its password

`wpfleet admin` stores a one-time token in the site through wp-cli and prints a link to it. A must-use plugin that the chart mounts answers the link once, within 60 seconds, and logs in as the site's first administrator, or as `--user`. No password is shared or reset, and the site stores only the hash of the token.

![The WordPress login page asks for a password; wpfleet admin prints a one-time link instead, and the link opens the dashboard](docs/images/admin-login.gif)

![The WordPress dashboard of a demo site, opened with a link from wpfleet admin](docs/images/admin-dashboard.png)

The notice at the top is expected: the chart turns off plugin, theme and core updates in the dashboard, because a site's code is its image, and a new version arrives through `wpfleet build` and `promote` instead.

The pictures come from a demo fleet of three invented sites on a local kind cluster. The browser frames are screenshots from headless Chromium, with the address bar drawn on them afterwards.

## Development

```bash
just build    # ./wpfleet
just test
just lint     # golangci-lint and helm lint
```

[docs/releasing.md](docs/releasing.md) says how a release is cut, what it publishes and how to verify its signatures and provenance.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the [code of conduct](CODE_OF_CONDUCT.md). Report a vulnerability privately, as [SECURITY.md](SECURITY.md) says.

## License

Apache-2.0, see [LICENSE](LICENSE).
