# wpfleet design

wpfleet manages a fleet of WordPress sites on Kubernetes: one Helm release per site per environment, from values files kept in Git. It grew out of a real migration of two dozen sites, where every step below was first a shell script. This file is the contract that the packages are built against; change it in the same commit as the code that changes the contract.

## What a fleet is

A fleet is a Git repository that holds `wpfleet.yaml` at its root and one values directory per environment:

```text
fleet/
├── wpfleet.yaml
└── environments/
    ├── development/
    │   ├── _shared.yaml      # values every site in the environment shares
    │   └── blog.yaml         # one file per site; the file name is the site name
    ├── staging/
    └── production/
```

A release is rendered from the chart with `_shared.yaml` first and the site file second, the way `helm -f _shared.yaml -f blog.yaml` does. The release name is the site name; each environment lives in its own namespace, so the names do not collide. Site files may hold secrets (the database password and the WordPress salts); the fleet repository is expected to be private, and wpfleet never prints those values.

`wpfleet.yaml`:

```yaml
# The chart: empty uses the one embedded in the binary (charts/wordpress).
chart: ""
registry: registry.example.com/sites      # images are <registry>/<site>:<tag>
build:
  base: docker.io/library/wordpress:7.0.2-php8.2-apache
wpcli:
  # Copied into the pod when a command needs wp-cli; pods often have no egress.
  url: https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar
  sha512: ""                               # checked when set
environments:
  - name: development
    namespace: wp-development
    kubeconfig: ""                         # empty: the usual KUBECONFIG rules
    context: ""
    values: environments/development       # default environments/<name>
  - name: staging
    namespace: wp-staging
    promoteFrom: development
  - name: production
    namespace: wp-production
    promoteFrom: staging
backup:
  s3:
    endpoint: https://s3.example.com
    bucket: wordpress-backups
    region: ""
    # Keys come from WPFLEET_S3_ACCESS_KEY_ID and WPFLEET_S3_SECRET_ACCESS_KEY,
    # never from this file.
```

Configuration loads with koanf: defaults, then `wpfleet.yaml`, then environment variables with the prefix `WPFLEET_` (`__` separates levels), then flags.

## Commands

Global flags: `--fleet, -f` (the fleet directory, default `.`, env `WPFLEET_FLEET`), `--env, -e` (the environment, env `WPFLEET_ENV`), `--output, -o` (`table`, the default, or `json`) and `--verbose, -v` (debug logs on stderr, env `WPFLEET_VERBOSE`). Output for people is a table; `--output json` gives JSON for scripts. A failing command prints `wpfleet: <error>` on stderr and exits 1; a command that needs another exit code without a message, like `diff`, returns `cli.Exit("", code)`. `wpfleet completion bash|zsh|fish` prints a shell completion script.

| Command | What it does |
| --- | --- |
| `wpfleet sites list` | Every site in one or all environments: site, environment, host, image, pod readiness. |
| `wpfleet sites show <site>` | One site in one environment: host, image, release revision, pod, node, volume, last backup. |
| `wpfleet deploy <site>... \| --all` | `helm upgrade --install` of each site. `--wait` waits for readiness. Never regenerates secrets. |
| `wpfleet diff <site>... \| --all` | What a deploy would change: the live manifest against the rendered one, hooks left out, blank lines ignored. Exit code 1 when there is a difference. |
| `wpfleet build <site> --source <dir> [--tag T]` | Builds `<registry>/<site>:<tag>` from the site's code tree on `build.base` and pushes it. The context goes to `docker build` as a tar stream (BuildKit refuses file names that are not UTF-8). Leaves out `wp-config.php`, `wp-content/uploads`, `wp-content/cache`, `wp-content/upgrade` and every `error_log`. `--set` writes the new image into the site file of `--env`. Default tag: UTC `YYYYMMDD-HHMMSS`. |
| `wpfleet promote <site>... --to <env>` | Copies `image.repository` and `image.tag` from the `promoteFrom` environment's site file into the target's, keeping comments and order. Does not deploy; it prints what changed. |
| `wpfleet admin <site>` | Prints, and with `--open` opens, a one-time login link to the site's admin, valid for 60 seconds. |
| `wpfleet backup run <site>` | Starts a Job from the site's backup CronJob, follows its logs, and fails when the Job fails. |
| `wpfleet backup list <site>` | The backups of a site in S3: stamp, database dump size, number of changed upload files. |
| `wpfleet backup restore <site> --stamp S [--uploads]` | Imports the dump from S3 into the site's database, and with `--uploads` syncs the uploads back, through a Job in the cluster. Asks for confirmation unless `--yes`. |
| `wpfleet copy <site> --from <env> --to <env>` | Restores the source's latest backup into the target, then moves the host with wp-cli `search-replace` and the page builder fix-up (see below). Refuses `--to production` unless `--yes-production`. |

## Packages

```text
cmd/wpfleet/main.go                    thin entrypoint: cmd.Execute()
charts.go                              package wpfleet: //go:embed all:charts/wordpress
internal/cmd/                          root command; Execute and Run print the error and set the exit code
internal/cmd/cmdutil/                  global flags, table/JSON output, loading the fleet and one environment
internal/cmd/sites/ deploy/ build/ promote/ admin/ backup/ copy/
internal/domain/fleet/                 Fleet, Environment, Site, host derivation, image refs, promotion plan
internal/domain/backup/                stamps, retention, restore plans (pure)
internal/infra/config/                 koanf loading of wpfleet.yaml
internal/infra/values/                 read and merge values files; edit a site file keeping comments
internal/infra/kube/                   client-go: pods of a site, exec, copy into a pod, Jobs from CronJobs, logs
internal/infra/kube/kubetest/          kube.Interface on the fake clientset with a recording exec, for tests
internal/infra/helm/                   Helm Go SDK: render, upgrade --install, live manifest, release status
internal/infra/wpcli/                  fetch and verify wp-cli.phar, copy it into a pod, run it
internal/infra/docker/                 docker build from a tar stream, docker push (os/exec)
internal/infra/s3/                     list backups (minio-go)
internal/infra/assets/                 embedded PHP: the page builder fix-up for copy
internal/testutil/                     assertions and file helpers the tests share
```

`internal/domain` imports nothing else from this module and does no I/O. `internal/infra` may import domain. `internal/cmd` wires them.

### Contracts between packages

- `fleet.Site` has `Name`, `Env` (an `Environment`) and `ValuesFiles []string` (shared first). `fleet.Host(values)` gives the canonical host from merged values exactly as the chart's `wordpress.hosts` helper does: the first of `domains`, else `<site><hostLabelSuffix>.<domainSuffix>`.
- `fleet.DiscoverSites(env, fileNames)` turns a listing of an environment's values directory into sites: every `*.yaml` except `_shared.yaml`, named by the file's stem, which must be a DNS label. `values.Sites(env)` reads the directory and calls it. `fleet.PlanPromotion(site, from, to, source, target)` compares the image of two site values and says what a promotion changes; `fleet.ParseImageRef` and `ImageRef.String` split and join `repository:tag@digest`. Domain code reads values through the small `fleet.Values` interface (`String`, `StringSlice`), which `values.Values` implements.
- `values.Load(files ...string) (Values, error)` merges like Helm (later files win, maps merge deeply, lists replace, an explicit null is kept for Helm). `Values` has `String(path)`, `StringSlice(path)`, `Bool(path)`, `Lookup(path)` and the generic `values.Get[T](v, path)`; paths are dot-separated. `values.SetImage(path, repository, tag string) error` edits one site file in place and keeps every comment, the key order and the rest of the file byte for byte; it adds a missing key or `image:` block, and refuses with `ErrUnsupportedLayout` a layout it cannot edit that way (a block scalar, an anchor, a flow mapping without the key) instead of rewriting the file.
- `kube.Client` is made per environment with `kube.New(kube.TargetFor(env))` (kubeconfig, context, namespace; the usual loading rules when the kubeconfig is empty). Pods of a site are selected by `app.kubernetes.io/instance=<site>` and `app.kubernetes.io/component` not equal to `backup`. Commands depend on the interfaces, not the struct: `kube.Execer` (`Exec`, `CopyFile`), `kube.Pods` (`SitePods`, `RunningPod`), `kube.Jobs` (`JobFromCronJob`, `WaitJob`, `JobPods`, `StreamLogs`) and `kube.Interface`, which is all three plus `Namespace()`. `Exec` streams over WebSocket and falls back to SPDY; `kube.ExitCode(err)` reads a command's non-zero exit code. `CopyFile` copies into the `wordpress` container through `tar -x`. `kubetest.New(namespace, objects...)` gives a `kube.Interface` on the fake clientset whose exec records every call, understands `tar -x` and `test -f`, and hands other commands to a handler the test sets.
- `helm.LoadChart(path)` loads a chart directory or archive, or the embedded chart when the path is empty. `helm.New(chart, target)` gives a client with `Render(ctx, site, namespace, valuesFiles)` (client-side, like `helm template`, hooks left out), `UpgradeInstall(ctx, site, namespace, valuesFiles, wait)` (installs when there is no release, else upgrades with the given values only; `wait` waits for readiness, otherwise only for hooks), `LiveManifest(site, namespace)` (hooks left out) and `Status(site, namespace)` (revision, status, chart version, updated). A missing release is `helm.ErrReleaseNotFound`. Releases are stored in Secrets, as the helm CLI does.
- `wpcli.Ensure(ctx, url, sha512)` downloads the phar once into the user's cache (`wpfleet/` under `os.UserCacheDir()`) and checks its SHA-512 when one is configured. `wpcli.NewRunner(kube, phar)` returns a runner whose `Run(ctx, pod, stdout, stderr, args...)` and `Output(ctx, pod, args...)` copy the phar into the pod once (as `/tmp/wp-cli.phar`, checked with `test -f`) and run `php /tmp/wp-cli.phar --allow-root --path=/var/www/html <args>` in the `wordpress` container.
- `cmdutil.OpenEnv(cmd)` loads `wpfleet.yaml` from `--fleet`, selects `--env`, discovers its sites and makes its kube client without contacting the cluster; the returned `Env` has `Sites`, `Kube` (a `kube.Interface`), `Site(name)`, `SelectSites(names, all)`, `Values(site)`, `Helm()` (loads the chart on first use) and `WPCLI(ctx, kube)`. `cmdutil.LoadFleet(cmd)` loads the fleet without choosing an environment. `cmdutil.Print(cmd, rows, columns...)` writes rows of any type as a table, or as JSON by their `json` tags with `--output json`.
- Configuration: `WPFLEET_BACKUP__S3__BUCKET` sets `backup.s3.bucket`, and so on. `WPFLEET_FLEET`, `WPFLEET_ENV`, `WPFLEET_OUTPUT`, `WPFLEET_VERBOSE` and the two S3 key variables are never read as configuration keys. Validation requires unique, non-empty environment names, a namespace for each, and a `promoteFrom` that names another environment.

## The chart

`charts/wordpress` is the chart every release uses. Its README lists the values. Two parts of it exist for wpfleet's commands:

- **The backup CronJob** (`backup.enabled`): every night it dumps the database with the site's own user and copies the dump and the uploads to S3 with rclone, under `<namespace>/<site>/database/<stamp>.sql.gz`, `<namespace>/<site>/uploads/current/` and `<namespace>/<site>/uploads/changed/<stamp>/`. It mounts the site's uploads claim, so with ReadWriteOnce node-local storage it runs on the node of the site's pod. Runs older than `keepDays` are deleted by the stamp in their name. The keys come from a Secret in the namespace with `access-key-id` and `secret-access-key`.
- **The one-time login** (`wordpress.oneTimeLogin`, on by default): a must-use plugin mounted from the ConfigMap. `wpfleet admin` stores a random token's SHA-256 with the user ID and an expiry of 60 seconds in an option through wp-cli; the plugin answers `/?wpfleet-login=<token>` once, deletes the option, sets the auth cookie and redirects to `/wp-admin/`. Only the hash is stored, and a token that is used or expired does nothing.

## Copying between environments

A host change needs more than `search-replace`. BeTheme keeps its page builder data base64-encoded in the post meta `mfn-page-items`, and WPML caches its switcher templates as objects of classes wp-cli does not load. `internal/infra/assets/rehost-extra.php` decodes and rewrites the first and deletes the second; `copy` runs it with `wp eval-file` after `search-replace "$old" "$new" --all-tables-with-prefix --skip-columns=guid --precise` (with `--network` on a multisite).

## Not in the first version

An operator with a `WordPressSite` resource, a web UI, image builds inside the cluster (BuildKit or Kaniko), single sign-on with OIDC, plugin vulnerability reports, and restore tests on a schedule. The CLI comes first, so that its model can settle before it becomes an API.
