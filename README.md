<h1><img src="docs/images/banner.svg" alt="wpfleet: WordPress sites on Kubernetes, run from one Git repository" width="100%"></h1>

<p align="center">
  <a href="https://github.com/rahacloud/wpfleet/actions/workflows/ci.yml"><img src="https://github.com/rahacloud/wpfleet/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/rahacloud/wpfleet"><img src="https://pkg.go.dev/badge/github.com/rahacloud/wpfleet.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/rahacloud/wpfleet"><img src="https://goreportcard.com/badge/github.com/rahacloud/wpfleet" alt="Go Report Card"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/rahacloud/wpfleet"><img src="https://api.scorecard.dev/projects/github.com/rahacloud/wpfleet/badge" alt="OpenSSF Scorecard"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/rahacloud/wpfleet" alt="License: Apache-2.0"></a>
</p>

wpfleet runs many WordPress sites on Kubernetes from one Git repository. Every site is a release of one shared Helm chart, its code is an immutable image, and every change goes through Git, so you always know what runs where and who changed it.

> [!NOTE]
> wpfleet is new and has no release yet. It works end to end on a test cluster, and its first runs on a real fleet are under way. Expect rough edges.

## What it does

- **One chart for every site.** A site is a small values file. `wpfleet diff` shows what a deploy would change before it changes anything.
- **Promote, do not edit live.** Plugins, themes and core ship in the site's image. You build it once and promote the same image from development to staging to production.
- **Admin without a password.** `wpfleet admin` gives a one-time link into a site's dashboard. Nobody shares or resets a password.
- **Backups to S3.** Nightly backups of the database and the uploads, a restore with one command, and a copy of a site from one environment to another.
- **A web UI for content teams.** Editors sign in with the company account, see only their own sites, open the admin, and publish or restore with one click. Every action is in an audit log.

## A quick look

```bash
wpfleet sites list                        # every site, its address, image and status
wpfleet diff blog -e development           # what a deploy would change
wpfleet deploy blog -e development
wpfleet promote blog --to staging && wpfleet deploy blog -e staging
wpfleet admin blog -e staging --open      # one-time login link
wpfleet backup run blog -e production
wpfleet copy blog --from production --to staging
```

![wpfleet lists a fleet, promotes a site from development to staging, deploys it and makes a login link](docs/images/demo.gif)

`wpfleet admin` opens the dashboard with a link that works once, for 60 seconds:

![The WordPress login page asks for a password; wpfleet admin prints a one-time link instead, and the link opens the dashboard](docs/images/admin-login.gif)

## The web UI

`wpfleet serve` runs inside your cluster, behind single sign-on with any OIDC provider. Each person sees only the sites that their team looks after, in plain words, with no image tags or commits. Engineers also see the technical details. A publish from the UI is a Git commit with the person as its author.

![An editor signs in, sees their site, presses "Open admin" and lands in the WordPress dashboard](docs/images/server-open-admin.gif)

![The home page of an editor: one site card with "Live site" and "Preview", each online, each with "Open admin"](docs/images/server-home.png)

The UI carries your organization's name, logo and colour. It is in English and Persian, and it works on a phone.

<p>
  <img src="docs/images/server-home-fa.png" width="62%" alt="The same page in Persian, right to left">
  <img src="docs/images/server-home-phone.png" width="18%" alt="The same page on a phone">
</p>

The screenshots come from a demo with invented sites, users and brand.

## Install

```bash
go install github.com/rahacloud/wpfleet/cmd/wpfleet@latest
```

Releases will also have signed binaries for Linux, macOS and Windows, a server image at `ghcr.io/rahacloud/wpfleet`, and the charts at `oci://ghcr.io/rahacloud/charts`. Then continue with [Getting started](docs/getting-started.md).

## Documentation

- [Getting started](docs/getting-started.md): make a fleet repository, deploy a first site, open its admin and back it up.
- [Configuration](docs/configuration.md): every key of `wpfleet.yaml` and of the server.
- [The WordPress chart](docs/chart.md): every value of the chart.
- [For content teams](docs/for-content-teams.md): the web UI in plain words, for editors.
- [Example fleet](examples/fleet): a small fleet with two environments and one site.
- [Design](docs/design.md): how wpfleet works inside.
- [Roadmap](docs/ROADMAP.md): what comes next.

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the [code of conduct](CODE_OF_CONDUCT.md), and report a vulnerability privately as [SECURITY.md](SECURITY.md) says.

wpfleet is made by [RahaCloud](https://rahacloud.com) and is licensed under [Apache-2.0](LICENSE).
