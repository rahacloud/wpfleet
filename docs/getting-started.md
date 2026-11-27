# Getting started

This guide is for the platform engineer who sets wpfleet up for a team. It takes you from an empty Git repository to a WordPress site that runs in Kubernetes, opens its admin without a password, and backs itself up every night. It uses only the `wpfleet` command line; the web UI (`wpfleet serve`) is not released yet, and [for-content-teams.md](for-content-teams.md) describes what it will give the people who edit the sites.

A complete example of everything below is in [examples/fleet](../examples/fleet). Every value that wpfleet reads from `wpfleet.yaml` is in [configuration.md](configuration.md), and every value of the WordPress chart is in [chart.md](chart.md).

## What you need

- **A Kubernetes cluster and a kubeconfig** that can manage the namespaces your sites will run in. wpfleet needs the rights of a namespace admin there (Deployments, Services, Ingresses, Secrets, ConfigMaps, PersistentVolumeClaims, CronJobs, Jobs, and Pods with `exec` and `log`); it never needs a cluster-wide role. wpfleet does not create namespaces, so create one per environment first.
- **A MariaDB or MySQL server** that the sites can reach from their namespace, for example a Service called `mariadb`. wpfleet does not install the database server. By default each site creates its own database and user before it installs, with the server's root password, which it reads from a Secret called `mariadb` with the key `root-password` in the same namespace. If you prefer to create the databases yourself, turn that off with `database.provision.enabled: false`.
- **An ingress controller** in the cluster, and either cert-manager with a ClusterIssuer or an existing TLS secret for the sites' hosts.
- **A container registry** that the cluster can pull from, and Docker on your machine, when you build site images with `wpfleet build`. You can start without them: a site can run the official WordPress image until it has an image of its own.
- **An S3-compatible bucket** for the nightly backups, with an access key that can read and write it.

## Install wpfleet

wpfleet is one static binary with the WordPress chart built in. Until the first release you build it from source; it needs the Go version that `go.mod` names.

```bash
git clone https://github.com/rahacloud/wpfleet.git
cd wpfleet
go build -o wpfleet ./cmd/wpfleet
sudo install wpfleet /usr/local/bin/
wpfleet --version
```

After the first release, the [GitHub releases page](https://github.com/rahacloud/wpfleet/releases) has archives for Linux, macOS and Windows, and `go install github.com/rahacloud/wpfleet/cmd/wpfleet@latest` works as well.

Shell completion is built in:

```bash
wpfleet completion zsh > "${fpath[1]}/_wpfleet"    # or bash, fish
```

## Make a fleet repository

A fleet is a Git repository with a `wpfleet.yaml` at its root and one directory of values files per environment. Site files hold the database password and the WordPress salts of each site, so keep the repository private.

```text
fleet/
├── wpfleet.yaml
└── environments/
    ├── development/
    │   ├── _shared.yaml      # values every site in development shares
    │   └── blog.yaml         # the site "blog"; the file name is the site name
    └── staging/
        ├── _shared.yaml
        └── blog.yaml
```

Start from the example:

```bash
cp -r wpfleet/examples/fleet ~/fleet
cd ~/fleet
git init
```

### wpfleet.yaml

`wpfleet.yaml` names the environments, the Kubernetes namespace of each, where images go, and where the backups are:

```yaml
registry: registry.example.com/sites      # images are <registry>/<site>:<tag>
environments:
  - name: development
    namespace: wp-development
  - name: staging
    namespace: wp-staging
    promoteFrom: development               # promote copies images from development
backup:
  s3:
    endpoint: https://s3.example.com
    bucket: wordpress-backups
```

An environment can point at its own cluster with `kubeconfig` and `context`; when both are empty, wpfleet follows the usual `KUBECONFIG` rules and uses the current context. Add a `production` environment the same way, with `promoteFrom: staging`. [configuration.md](configuration.md) lists every key.

### The shared values of an environment

`environments/<env>/_shared.yaml` holds what every site of that environment has in common: how hosts are named, the ingress, the database server and the backups.

```yaml
domainSuffix: example.com
hostLabelSuffix: -dev            # blog is served on blog-dev.example.com
database:
  host: mariadb
ingress:
  className: nginx
  tls:
    clusterIssuer: letsencrypt
```

A site without `domains` is served on `<site><hostLabelSuffix>.<domainSuffix>`, so development and staging need no per-site host at all. In production, set each site's real domains in its own file with `domains: [www.example.com, example.com]`; the first one is the canonical host.

### A site file

`environments/<env>/<site>.yaml` holds what is only this site's. The file name, without `.yaml`, is the site's name, and it must be a DNS label (lowercase letters, digits and hyphens). Set `site` to the same name.

```yaml
site: blog
database:
  password: <32 random letters and digits>
salts:
  authKey: <random>
  secureAuthKey: <random>
  loggedInKey: <random>
  nonceKey: <random>
  authSalt: <random>
  secureAuthSalt: <random>
  loggedInSalt: <random>
  nonceSalt: <random>
```

The database password must be at least 16 letters and digits, with no other characters, because it passes through YAML, shell, SQL and PHP. Generate the password and the eight salts once per site and per environment, for example with `openssl rand -hex 16` for the password and `openssl rand -hex 32` for each salt. wpfleet never generates or prints them, and a deploy uses them exactly as the file says. Do not regenerate them for a running site without a reason: new salts log every user out, and a new password works only while the provisioning hook (`database.provision`) can set it on the database server.

Leave `image` out at first, as the example does, and the site runs the official WordPress image that the chart names. `wpfleet build --set` adds the image block later (see below).

### Check what wpfleet sees

```bash
wpfleet sites list
```

```text
SITE  ENV          HOST                      IMAGE                                            READY
blog  development  blog-dev.example.com      docker.io/library/wordpress:7.0.2-php8.4-apache  0/0
blog  staging      blog-staging.example.com  docker.io/library/wordpress:7.0.2-php8.4-apache  0/0
```

This is the example fleet before anything is deployed: no pods yet, so READY is `0/0`. `sites list` reads the values files and asks each environment's cluster how many of the site's pods are ready. If it cannot reach a cluster, it prints a warning and shows READY as `unknown`, and the rest of the list still prints, so you can check a new fleet before any cluster is set up. Run it from the fleet directory, or point at the fleet from anywhere with `--fleet ~/fleet` or `WPFLEET_FLEET`.

## The first deploy

Look at what a deploy would create, then deploy:

```bash
wpfleet diff blog -e development          # the whole release, since none exists yet
wpfleet deploy blog -e development --wait
```

`deploy` runs `helm upgrade --install` with the embedded chart, `_shared.yaml` first and the site file second. Before WordPress starts, a hook creates the site's database and user. `--wait` waits until the site is ready. `--all` deploys every site of the environment, one after another; a site that fails does not stop the others, and the command fails at the end naming every failed site.

Every deploy writes the fleet repository's commit into the Helm release (`wpfleet <sha>`, or `wpfleet <sha>-dirty` when the files had uncommitted changes), so you can always tell which commit a site runs. Commit before you deploy and the history stays clean.

`diff` exits with code 1 when a deploy would change something, so it fits in CI as a drift check. Secret values never print: each shows as a tag that changes when the value changes.

The first time you open the site, WordPress asks for the site's title and the first administrator, as on any new install. wpfleet has no import command for a site that lives somewhere else today: load its database into the site's database and its uploads into the site's uploads volume yourself, and build its code into an image with `wpfleet build`. Once a site has run its first backup, `backup restore` and `copy` (below) move its data around.

## Open a site's admin

```bash
wpfleet admin blog -e development --open
```

`admin` prints a link that logs you in to the site's dashboard once, within 60 seconds, as the site's first administrator, or as `--user` (a login, an email or a user ID). No password is shared or reset. `--open` opens the link in your browser; without it, send the link to whoever needs it, quickly.

It works through a small must-use plugin that the chart mounts into every site (`wordpress.oneTimeLogin`, on by default) and wp-cli, which wpfleet downloads once to your machine, checks against its published SHA-512, and copies into the site's pod. Your machine needs to reach GitHub for that first download, but the pods do not. Turn `wordpress.oneTimeLogin` off for a site where nobody should get in without a password.

You will see a notice in the dashboard that updates are turned off. That is on purpose: a site's code is its image, and plugins, themes and WordPress itself change through a new image (see the next section), not through the dashboard.

## Build an image and promote it

A site's code (WordPress core, its themes and its plugins) is an immutable image built on `build.base`. From the site's code tree:

```bash
docker login registry.example.com
wpfleet build blog --source ./blog-code -e development --set
wpfleet deploy blog -e development --wait
```

`build` leaves out of the image what holds secrets or is written at run time: `wp-config.php` and its copies, database dumps, `wp-content/uploads`, `wp-content/cache`, `.git`, `.env` and the like. `--set` writes the new image into the site's file in `development` after the push succeeds. If the registry is private, give the cluster a pull secret and list it in `imagePullSecrets`.

When the image works in development, move it on:

```bash
wpfleet promote blog --to staging
git commit -am "Promote blog to staging"
wpfleet deploy blog -e staging --wait
```

`promote` copies `image.repository` and `image.tag` from the environment that `--to` promotes from, keeping every comment in the file. It does not deploy, so you can review and commit the change first.

## Back up and restore

Every site can run a nightly backup to the bucket: a dump of its database and a mirror of its uploads, plus what each night replaced or deleted, kept for `backup.keepDays` (14 by default).

1. Make a Secret with the bucket's keys in every namespace that has sites:

   ```bash
   kubectl -n wp-development create secret generic backup-s3 \
     --from-literal=access-key-id=EXAMPLEKEY \
     --from-literal=secret-access-key=EXAMPLESECRET
   ```

2. Turn the backup on in `_shared.yaml` and name the bucket as the cluster reaches it:

   ```yaml
   backup:
     enabled: true
     s3:
       endpoint: https://s3.example.com
       bucket: wordpress-backups
       secretName: backup-s3
   ```

3. Deploy the sites again to create their backup CronJobs.

The backup runs at `backup.hour` (3 by default) in `backup.timeZone`, at a minute derived from the site's name so the sites do not all start at once. To run one now and see the result:

```bash
wpfleet backup run blog -e development
```

To list a site's backups and restore one, wpfleet reads the bucket from your machine, with the keys in two environment variables (never in `wpfleet.yaml`):

```bash
export WPFLEET_S3_ACCESS_KEY_ID=EXAMPLEKEY
export WPFLEET_S3_SECRET_ACCESS_KEY=EXAMPLESECRET
wpfleet backup list blog -e development
wpfleet backup restore blog -e development --stamp latest --uploads
```

A restore asks for confirmation (skip it with `--yes`), then runs a Job in the site's namespace that imports the dump and, with `--uploads`, puts the uploads back. With `--uploads` the site must be running, because its uploads volume can attach to one node only.

To give staging a fresh copy of production, `copy` restores the source's latest backup into the target and moves the site to the target's host, with wp-cli `search-replace` and a fix-up for page builder data that `search-replace` cannot see:

```bash
wpfleet copy blog --from production --to staging
```

`copy` refuses `--to production` unless you add `--yes-production`. Both environments must write to the same bucket, and the target namespace's key must be able to read the source's backups.

## Scripts and CI

Every command prints a table for people and JSON with `--output json`. Errors go to stderr as `wpfleet: <error>` with exit code 1. The global flags have environment variables too: `WPFLEET_FLEET`, `WPFLEET_ENV` and `WPFLEET_VERBOSE`. A common CI job runs `wpfleet diff --all -e production` and fails when the cluster has drifted from the repository.

## Where to go next

- [configuration.md](configuration.md): every key of `wpfleet.yaml` and how environment variables override them.
- [chart.md](chart.md): every value of the WordPress chart, grouped by purpose.
- [design.md](design.md): how wpfleet works inside, and what the coming server will do.
