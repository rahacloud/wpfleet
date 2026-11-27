# Security policy

wpfleet holds the keys to a fleet of WordPress sites: it deploys them, reads their secrets and logs in to their admin. We take a report about it seriously and thank everyone who sends one.

## Report a vulnerability

Report it privately through GitHub: open the repository's [Security tab](https://github.com/rahacloud/wpfleet/security) and choose **Report a vulnerability**, or go straight to the [new advisory form](https://github.com/rahacloud/wpfleet/security/advisories/new). Only the maintainers see the report.

Do not open a public issue, a pull request or a discussion about a vulnerability before it is fixed.

Tell us what you found, the version or commit, how to reproduce it, and what an attacker gains. A proof of concept helps; test it against your own sites and cluster only.

## What happens next

- We confirm that we received the report within 5 working days.
- We tell you whether we accept it, and our plan, within 14 days.
- We fix it in a private fork, publish a release with the fix, and then publish the advisory. With your agreement, the advisory credits you.

We ask you to keep the details private until the advisory is published, and for no longer than 90 days from your report unless we agree on more time together.

## Supported versions

| Version | Supported |
| --- | --- |
| Before v0.1 | Only the `main` branch. |
| v0.x | The latest minor release. A fix lands in a new patch release of it. |
| v1 and later | The latest minor release. This table will say more before v1. |

## Scope

In scope is the code in this repository and what it installs:

- **The server's authentication and authorisation** (`wpfleet serve`): the OpenID Connect sign-in, the session cookie, the CSRF protection, the mapping from groups to the environments and actions a person may use, the Git webhook's signature check, and the audit log. A way to act without signing in, to act beyond one's groups, or to act without an audit record is a vulnerability.
- **The one-time admin login**: the token that `wpfleet admin` and the server store in a site, and the must-use plugin that the chart mounts to answer it. A way to log in without a valid token, to use a token twice or after it expires, to read the token from the site, or to choose the user beyond what the design allows is a vulnerability.
- **Secrets handling**: database passwords, WordPress salts, S3 keys and the server's own keys. wpfleet never prints them in output, logs, errors or diffs, and `wpfleet build` leaves them out of the site's image. A path where one leaks is a vulnerability.
- **The WordPress chart in `charts/wordpress`**: a default that exposes a site, its database or its secrets beyond what the values ask for.
- **The release artifacts**: the binaries, the archives and the container image, and how they are built.

Out of scope:

- Vulnerabilities in WordPress core, themes or plugins that a fleet runs. Report them to their authors.
- A cluster, an ingress, an identity provider or a Git host that its operator configured insecurely.
- Secrets that an operator commits to a public fleet repository. The fleet repository is expected to be private, as [docs/design.md](docs/design.md) says.
- What wpfleet is designed to do for a person who is allowed to do it: a person whose groups allow deploys to production can deploy to production.
- Reports from automated scanners without a demonstrated impact.
