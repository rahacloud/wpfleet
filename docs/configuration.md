# Configuration

`wpfleet.yaml` sits at the root of a fleet repository and describes the fleet: the chart, the registry, the environments and the backup bucket. This page lists every key. The values of each site (its host, image, database, backups and the rest) are not here; they live in the environment's values files and are described in [chart.md](chart.md).

## How configuration loads

wpfleet builds its configuration in this order, each layer over the one before:

1. the defaults in the tables below;
2. `wpfleet.yaml` in the fleet directory (`--fleet`, default `.`);
3. environment variables that start with `WPFLEET_`, with `__` between levels: `WPFLEET_REGISTRY` sets `registry`, and `WPFLEET_BACKUP__S3__BUCKET` sets `backup.s3.bucket`;
4. command-line flags.

A missing `wpfleet.yaml` is an error that names the directory, so a command run from the wrong place says so. After loading, wpfleet checks that every environment has a name and a namespace, that no name appears twice, and that every `promoteFrom` names another environment that exists.

A few `WPFLEET_` variables are never configuration keys, because they belong to a flag or hold a secret:

| Variable | Meaning |
| --- | --- |
| `WPFLEET_FLEET` | The fleet directory, like `--fleet`. |
| `WPFLEET_ENV` | The environment, like `--env`. |
| `WPFLEET_OUTPUT` | Not read as configuration; use `--output`. |
| `WPFLEET_VERBOSE` | Debug logs on stderr, like `--verbose`. |
| `WPFLEET_S3_ACCESS_KEY_ID` | The access key of the backup bucket, for `backup list`, `backup restore` and `copy`. |
| `WPFLEET_S3_SECRET_ACCESS_KEY` | The secret key of the backup bucket. |

Secrets never go in `wpfleet.yaml`. The S3 keys come only from the two variables above, and the coming server reads its own secrets from variables as well (see the `server` section below).

## Top level

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `chart` | string | `""` | The WordPress chart every release uses: a chart directory or a packaged archive, relative to the fleet directory unless absolute. Empty uses the chart embedded in the wpfleet binary, which is what [chart.md](chart.md) describes. Set it only when you maintain a chart of your own. |
| `registry` | string | `""` | Where `wpfleet build` pushes site images, as `<registry>/<site>:<tag>`, for example `registry.example.com/sites`. Only `build` needs it. |
| `build` | map | | Image builds; see below. |
| `wpcli` | map | | Where wpfleet gets wp-cli; see below. |
| `environments` | list | none | The fleet's environments, in order; see below. |
| `backup` | map | | The backup bucket; see below. |

## build

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `build.base` | string | `docker.io/library/wordpress:7.0.2-php8.4-apache` | The image every site image is built on. `wpfleet build` copies the site's code tree into `/var/www/html` on top of it. |

## wpcli

`admin` and `copy` run wp-cli inside a site's pod. wpfleet downloads the phar once to your machine (into `wpfleet/` under your user cache directory), checks its SHA-512, and copies it into the pod, so the pods need no internet access.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `wpcli.url` | string | the wp-cli 2.12.0 release on GitHub | The download URL of `wp-cli.phar`. |
| `wpcli.sha512` | string | the published SHA-512 of the default phar | The phar's SHA-512 in hex. A phar that does not match is refused. Change it together with `url`. |
| `wpcli.allowUnverified` | bool | `false` | Lets an empty `sha512` run the phar unchecked. Leave it off unless you have no published sum to check against. |

## environments

Each entry is one environment, usually `development`, `staging` and `production`. Each environment's sites run in one namespace, and every site is one Helm release named after the site.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `name` | string | required | The environment's name, as `--env` takes it. Unique. |
| `namespace` | string | required | The Kubernetes namespace of the environment's sites. wpfleet does not create it. |
| `kubeconfig` | string | `""` | A kubeconfig file, relative to the fleet directory unless absolute. Empty follows the usual rules: `KUBECONFIG`, then `~/.kube/config`. Inside the cluster, the coming server uses its ServiceAccount when this is empty. |
| `context` | string | `""` | A context in that kubeconfig. Empty uses the current context. |
| `values` | string | `environments/<name>` | The directory of the environment's values files, relative to the fleet directory. It holds `_shared.yaml` and one `<site>.yaml` per site; every `*.yaml` file other than `_shared.yaml` is a site, named by the file name, which must be a DNS label. |
| `promoteFrom` | string | `""` | The environment that `wpfleet promote --to <name>` copies images from. Empty means nothing promotes into this environment. It must name another environment. |

## backup

The nightly backups themselves are made by a CronJob in each site's release, configured with the chart's `backup` values (see [chart.md](chart.md)). This section tells the commands that read the bucket from your machine (`backup list`, `backup restore` and `copy`) where it is.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `backup.s3.endpoint` | string | `""` | The URL of the S3-compatible service as your machine reaches it, for example `https://s3.example.com`. A restore Job uses the site's own `backup.s3.endpoint` value when it is set, because the cluster may reach the service by another address, and this one otherwise. |
| `backup.s3.bucket` | string | `""` | The bucket's name. It must be the bucket the sites' CronJobs write to. |
| `backup.s3.region` | string | `""` | The bucket's region; most S3-compatible services do not need one. |

## server (coming with wpfleet serve)

`wpfleet serve`, a web UI that runs the same operations for people who sign in with single sign-on, is being built and is not in a release yet. This section is written from [design.md](design.md) and may change before it ships; until then wpfleet does not read it.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `server.listen` | string | `:8080` | The address the web UI listens on. |
| `server.metricsListen` | string | `:9090` | The address of the metrics endpoint. |
| `server.publicURL` | string | | The URL people open, for example `https://wpfleet.example.com`. The single sign-on redirect is `<publicURL>/auth/callback`. |
| `server.git.url` | string | | The fleet repository the server clones, for example `git@github.com:example/fleet.git`. |
| `server.git.branch` | string | `main` | The branch the server deploys from and pushes promotions to. |
| `server.git.pollInterval` | duration | `1m` | How often the server fetches the branch. A push webhook at `POST /hooks/git` makes it fetch at once. |
| `server.git.committer.name` | string | | The committer name of the server's commits; the signed-in person is the author. |
| `server.git.committer.email` | string | | The committer email of the server's commits. |
| `server.oidc.issuer` | string | | The OpenID Connect issuer, for example `https://sso.example.com/realms/main`. Any provider that publishes discovery works: Keycloak, Dex, Azure AD, Google, and GitHub through Dex. |
| `server.oidc.clientID` | string | | The OIDC client ID. |
| `server.oidc.groupsClaim` | string | `groups` | The ID token claim that holds the person's groups, which the access rules match. |
| `server.session.maxAge` | duration | `8h` | How long a sign-in lasts. Groups are read again at the next sign-in. |
| `server.admin.matchEmail` | bool | `true` | "Open admin" logs in as the WordPress administrator whose email is the signed-in person's, when there is one, so the site's own records name the right person. |
| `server.access` | list | none | The access rules. Access is denied unless a rule allows it. |

Each rule in `server.access` has these keys:

| Key | Type | Meaning |
| --- | --- | --- |
| `group` | string | The group, from the groups claim, that the rule applies to. |
| `environments` | list of strings | The environments the rule covers, by name, or `*` for all. |
| `sites` | list of strings | The sites the rule covers, by name, or `*` for all. |
| `allow` | list of strings | The permissions it gives: `view` (see the site, its details, history and backups), `deploy` (promote into the environment and deploy there), `login` (the one-time admin login), `backup` (run a backup now) and `restore` (restore a backup over the site). Every permission includes `view`. |

A person's permissions are the union of every rule that matches one of their groups. For example:

```yaml
server:
  publicURL: https://wpfleet.example.com
  git:
    url: git@github.com:example/fleet.git
    branch: main
    committer: {name: wpfleet, email: wpfleet@example.com}
  oidc:
    issuer: https://sso.example.com/realms/main
    clientID: wpfleet
  access:
    - group: platform
      environments: ["*"]
      sites: ["*"]
      allow: [view, deploy, login, backup, restore]
    - group: shop-team
      environments: [development, staging]
      sites: [shop]
      allow: [view, login]
```

The server's secrets come from environment variables, never from `wpfleet.yaml`:

| Variable | Meaning |
| --- | --- |
| `WPFLEET_OIDC_CLIENT_SECRET` | The OIDC client secret. |
| `WPFLEET_SESSION_KEY` | 32 random bytes in base64, which encrypt the session cookie. |
| `WPFLEET_GIT_SSH_KEY` | The path of an SSH deploy key for the fleet repository. |
| `WPFLEET_GIT_TOKEN` | An HTTPS token for the fleet repository, instead of an SSH key. |
| `WPFLEET_GIT_WEBHOOK_SECRET` | The secret that signs push webhooks (HMAC-SHA256, in the GitHub and Gitea format). |

## A complete example

[examples/fleet/wpfleet.yaml](../examples/fleet/wpfleet.yaml) is a working file with two environments, and [design.md](design.md) shows one with every key, `production` included.
