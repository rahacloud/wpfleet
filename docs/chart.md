# The WordPress chart

Every site in every environment is one release of the WordPress chart in [charts/wordpress](../charts/wordpress), which is built into the wpfleet binary. A release is rendered from the environment's `_shared.yaml` first and the site's own file second, so a value in the site file wins over the same value in `_shared.yaml`, and both win over the chart's defaults. Maps merge key by key; a list in a later file replaces the whole list.

This page groups the chart's values by purpose. [charts/wordpress/values.yaml](../charts/wordpress/values.yaml) is the reference, with a comment on every value. As a rule of thumb, put in `_shared.yaml` what all sites of an environment share (host naming, ingress, database server, backups) and in the site file what is the site's own (its name, domains, image, password and salts).

## Identity and hosts

| Value | Default | Meaning |
| --- | --- | --- |
| `site` | `""` | Required. The site's short name: lowercase letters, digits and hyphens. It names the database, the database user, the Kubernetes objects and the default host. Set it to the site file's name. |
| `domains` | `[]` | The site's public hostnames, canonical first. The canonical host is the one WordPress and wpfleet use for links. |
| `domainSuffix` | `""` | When `domains` is empty, the site is served on `<site><hostLabelSuffix>.<domainSuffix>`. One of `domains` and `domainSuffix` is required. |
| `hostLabelSuffix` | `""` | Added to the site name inside the first label, for example `-dev` for `blog-dev.example.com`, so that environments can share one domain and one wildcard certificate, which covers one label only. Lowercase letters, digits and hyphens. |

## Image and size

| Value | Default | Meaning |
| --- | --- | --- |
| `image.repository` | `docker.io/library/wordpress` | The site's image. `wpfleet build --set` and `wpfleet promote` write this and `image.tag` in the site file. |
| `image.tag` | `7.0.2-php8.4-apache` | The image's tag. |
| `image.pullPolicy` | `IfNotPresent` | Kubernetes' image pull policy. |
| `imagePullSecrets` | `[]` | Secrets for a private registry, as Kubernetes takes them: `[{name: registry-pull}]`. |
| `replicas` | `1` | The number of pods. More than one needs `uploads.accessMode: ReadWriteMany`; rendering refuses otherwise. |
| `autoscaling.enabled` | `false` | Scale the pods on CPU with a HorizontalPodAutoscaler. Needs `ReadWriteMany` uploads as well. |
| `autoscaling.minReplicas` | `2` | The fewest pods when autoscaling. |
| `autoscaling.maxReplicas` | `6` | The most pods when autoscaling. |
| `autoscaling.targetCPUUtilizationPercentage` | `70` | The CPU use the autoscaler aims for. |
| `resources` | requests `100m` CPU and `256Mi` memory, limit `512Mi` memory | The WordPress container's resources. |
| `nodeSelector` | `{}` | Node labels the pods must run on. |
| `tolerations` | `[]` | Taints the pods tolerate. |
| `podSecurityContext.fsGroup` | `33` | The group that owns the uploads volume; 33 is `www-data` in the official image, which lets WordPress write uploads. |

## Database

The site uses a MariaDB or MySQL server that the chart does not install.

| Value | Default | Meaning |
| --- | --- | --- |
| `database.host` | `mariadb` | The database server's host, usually a Service in the namespace. |
| `database.port` | `3306` | Its port. |
| `database.name` | `""` | The database's name. Empty is `wp_<site>`, with hyphens turned into underscores. |
| `database.user` | `""` | The database user. Empty is the same as the name; at most 32 characters. |
| `database.password` | `""` | Required. At least 16 letters and digits, nothing else, because it passes through YAML, shell, SQL and PHP. |
| `database.tablePrefix` | `wp_` | WordPress's table prefix. |
| `database.provision.enabled` | `true` | Before every install and upgrade, a hook creates the database and the user, and sets the user's password, with the server's root password. Turn it off to create them yourself. |
| `database.provision.rootSecret.name` | `mariadb` | The Secret in the namespace that holds the root password. |
| `database.provision.rootSecret.key` | `root-password` | The key of the root password in that Secret. |
| `database.provision.image` | `docker.io/library/mariadb:12.3.3` | The image of the provisioning hook. The backup dump and the restore import use it too. |

## Salts

| Value | Default | Meaning |
| --- | --- | --- |
| `salts.authKey`, `salts.secureAuthKey`, `salts.loggedInKey`, `salts.nonceKey`, `salts.authSalt`, `salts.secureAuthSalt`, `salts.loggedInSalt`, `salts.nonceSalt` | `""` | Required, all eight: WordPress's keys and salts. Use long random strings, different for every site and environment. Changing any of them logs every user out. |

## Uploads

`wp-content/uploads` is the only state a site keeps on disk. It lives on a PersistentVolumeClaim that stays when the release is uninstalled.

| Value | Default | Meaning |
| --- | --- | --- |
| `uploads.size` | `10Gi` | The volume's size. |
| `uploads.storageClassName` | `""` | The storage class. Empty uses the cluster's default. |
| `uploads.accessMode` | `ReadWriteOnce` | With `ReadWriteOnce` the site runs as one pod, replaced with the Recreate strategy, because a second pod on another node cannot attach the volume. `ReadWriteMany` allows more replicas and autoscaling, when the storage class supports it. |
| `uploads.existingClaim` | `""` | Use a claim that already exists instead of creating one. |

## Backups

A nightly CronJob dumps the database with the site's own user and copies the dump and the uploads to an S3-compatible bucket with rclone. In the bucket, a site's backups are under `<namespace>/<site>/`: `database/<stamp>.sql.gz`, a mirror of the uploads in `uploads/current/`, and what each night replaced or deleted in `uploads/changed/<stamp>/`. The backup pod runs on the node of the site's pod, because it mounts the site's uploads volume. `wpfleet backup run`, `list` and `restore` work with these backups; see [getting-started.md](getting-started.md).

| Value | Default | Meaning |
| --- | --- | --- |
| `backup.enabled` | `false` | Create the backup CronJob. |
| `backup.schedule` | `""` | A cron schedule. Empty runs every day at `backup.hour`, at a minute derived from the site's name, so the sites do not all start at the same moment. |
| `backup.hour` | `3` | The hour of the default schedule. |
| `backup.timeZone` | `Etc/UTC` | An IANA time zone for the schedule. Empty uses the cluster's. |
| `backup.keepDays` | `14` | Dumps and replaced upload files older than this are deleted. |
| `backup.image` | `docker.io/rclone/rclone:1.72.0` | The rclone image. |
| `backup.s3.endpoint` | `""` | Required when backups are on. The S3 service's URL as the cluster reaches it. |
| `backup.s3.bucket` | `""` | Required when backups are on. The bucket; the same as `backup.s3.bucket` in `wpfleet.yaml`. |
| `backup.s3.secretName` | `backup-s3` | A Secret in the namespace, made by hand, with the keys `access-key-id` and `secret-access-key`. |

## PHP

| Value | Default | Meaning |
| --- | --- | --- |
| `php.uploadMaxFilesize` | `64M` | The largest file a person can upload. |
| `php.memoryLimit` | `256M` | PHP's memory limit. |
| `php.maxExecutionTime` | `300` | How many seconds a request may run. |
| `php.timezone` | `UTC` | PHP's `date.timezone`, which scheduled posts and PHP's log timestamps use. An IANA name. |
| `php.extraIni` | `""` | Lines added as they are to the php.ini fragment. |

## WordPress

| Value | Default | Meaning |
| --- | --- | --- |
| `wordpress.debug` | `false` | Turns on `WP_DEBUG`. |
| `wordpress.disallowFileMods` | `true` | Stops the dashboard from installing, updating or editing plugins, themes and WordPress itself, because they ship in the image. Turn it off only in development, knowing that whatever the dashboard installs disappears when the pod restarts. |
| `wordpress.blockExternalHttp` | `false` | Makes WordPress refuse every outside host at once (`WP_HTTP_BLOCK_EXTERNAL`) instead of waiting for a timeout. Turn it on where the pods have no internet access; otherwise the login page alone can be slower than the readiness probe allows. |
| `wordpress.accessibleHosts` | `[]` | Hosts still allowed when `blockExternalHttp` is on, wildcards allowed, for example a payment gateway. |
| `wordpress.configExtra` | `""` | PHP added to `wp-config.php`. |
| `wordpress.oneTimeLogin` | `true` | Mounts the small must-use plugin that answers `wpfleet admin`'s one-time login links. It opens nothing by itself: it only accepts a token that wp-cli stored in the site's database a moment before, once, within 60 seconds. Turn it off where nobody should get in without a password. |
| `wordpress.htaccess` | `""` | When set, replaces the image's `.htaccess` in this environment only, read-only, for example to drop an IP allowlist that came from the old host. |

## Ingress and TLS

| Value | Default | Meaning |
| --- | --- | --- |
| `ingress.enabled` | `true` | Create an Ingress with one rule per host. |
| `ingress.className` | `""` | The ingress class, for example `nginx` or `traefik`. Empty uses the cluster's default. |
| `ingress.annotations` | `{}` | Annotations for the Ingress. |
| `ingress.tls.enabled` | `true` | Serve the site over HTTPS. When it is off, the site's URL is `http://`. |
| `ingress.tls.clusterIssuer` | `""` | A cert-manager ClusterIssuer that issues the certificate into `<site>-tls`. Empty sets no issuer. |
| `ingress.tls.secretName` | `""` | An existing TLS secret, for example a wildcard certificate that every site of an environment shares. It replaces `<site>-tls`, and cert-manager is left out. |

## Probes

| Value | Default | Meaning |
| --- | --- | --- |
| `probes.readinessPath` | `/wp-login.php` | The path the readiness probe loads. It loads WordPress and so touches the database; the liveness probe only checks that the web server answers. |
