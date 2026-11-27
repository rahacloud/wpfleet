# Roadmap

This is where wpfleet is going after its first release. It is a plan, not a promise: the order can change when people who run fleets tell us what hurts most. Every item keeps the model that wpfleet is built on: a site's code is an immutable image, Git is the source of truth, the WordPress dashboard never installs or updates code, every site is one release of one chart, and the server keeps no database. Sizes are S (days), M (one to two weeks) and L (more). Open an issue to argue for or against an item.

## Now (v0.1 → v0.2)

**Site inventory.** `wpfleet build` records the core, plugin and theme versions of the code tree (from the file headers, or `composer.lock` for a Bedrock tree) in the image, and checks core and plugin checksums against WordPress.org before it pushes. `sites show` and the server's technical details list them. Every update and security feature below reads this inventory instead of running commands in a live site. (S)

**Vulnerability report.** `wpfleet vulns` matches every image's inventory against a vulnerability feed and lists the site, the component, the installed and fixed versions and the severity. The default feed is Wordfence Intelligence v3, which is free for commercial use with your own API key; Patchstack and WPScan are optional for those with a licence. The server shows the count on the platform team's cards and exports it as a metric. (M)

**Monitoring wired into your cluster.** Optional chart output for the Prometheus Operator: a blackbox `Probe` per host, and alert rules for a site that is down, a certificate that expires within 14 days, a failed backup and a backup older than two days. The server adds per-site gauges for image age, vulnerabilities, last backup and pending publications. wpfleet does not install Prometheus; it plugs into the one you run. (S)

**Notifications.** The server sends publish, deploy, restore, failed backup and new critical vulnerability events to a JSON webhook, Slack or Telegram, routed per environment or site. (S–M)

**Protected non-production environments.** Chart values that keep staging and development out of search engines (`X-Robots-Tag: noindex` and `blog_public` off) and, optionally, behind basic authentication at the ingress. (S)

**WP-Cron as a CronJob.** An opt-in chart value that turns off page-load cron and runs due events from a CronJob with wp-cli, so scheduled posts publish on a quiet staging site and busy live sites do not run cron on visits. (S)

**A safety backup before every overwrite.** `backup restore` and `copy` back up the target first and print the stamp, so a mistaken restore is one command away from undone. (S)

**`wpfleet logs`.** Stream a site's web server and PHP logs for operators and developers. (S)

## Next

**Updates as pull requests.** `wpfleet updates` finds new versions of each site's core, plugins and themes, waits a minimum release age (24 hours by default), and opens a pull request on the site's code with the changelog and the vulnerabilities the update fixes. CI builds the image for development, and `promote` moves it up like any other change. Bedrock sites can use Renovate with WP Packages instead. Without this, sites that cannot update themselves fall behind, so it is the most important item after the first release. Premium plugins that are not on WordPress.org stay manual unless their vendor offers a Composer repository. (L)

**Visual checks before publishing.** Before a site is published from one environment to the next, a Job takes screenshots of a few configured pages in both with Playwright, compares them with a threshold and masked areas for dynamic content, and keeps the report in the backup bucket. The server shows the verdict next to "Publish". (M–L)

**Anonymized copies.** `copy` into a non-production environment replaces personal data (user emails and passwords, sessions, shop customers) with built-in rules plus your own rules in `wpfleet.yaml`, unless you pass `--no-sanitize`. (M)

**Import an existing site.** `wpfleet import` takes a site from another host, as a files tree and a SQL dump, and makes it a fleet site: the code tree for `build`, a site file with fresh salts and passwords, the database, the uploads and the host move. It generalizes the migration tooling that wpfleet grew out of. (M)

**Fleet health report.** `wpfleet report` and a page for the platform team: per site, readiness, image age, core and PHP versions, vulnerabilities, last backup, certificate expiry, pending publications and a few hardening checks, as a table, JSON, or Markdown for a monthly summary. (M)

**Two-person publishing.** For chosen environments, a publish in the server opens a pull request instead of pushing, the Git host's branch protection asks for a second approval, and the server deploys on merge. The approval lives in Git, not in a wpfleet database. (M)

**Restore tests on a schedule.** Every week, restore each site's latest backup into a throwaway database, check that WordPress loads it, and report the result through metrics and notifications. (M)

## Later

**Preview environments for pull requests:** an environment per pull request with an anonymized copy of staging and the pull request's image, removed when it closes. (L) **CDN purge** after deploy and restore, and an option to use an existing Redis as the object cache. (M) **Publish windows** that refuse a publication to live outside the hours you set. (S) **Subdomain multisite** with wildcard hosts and certificates. (M) **Extra search-replace pairs** per site for hard-coded hosts. (S) **Offboarding:** find and lock one person's WordPress accounts on every site at once. (M) **Single sign-on in front of staging** through the wpfleet server. (M) **A read-only MCP server** so AI tools can answer questions about the fleet under the same access rules. (S) **Signed site images** with the inventory attached as an SBOM, and a server option to refuse unsigned images on live. (M) **An operator** with a `WordPressSite` resource, once the server needs to reconcile environments it did not start (preview environments would be that moment). (L) **Image builds inside the cluster** with BuildKit or Kaniko, for teams whose CI or workstations cannot run Docker; `build` keeps the local Docker build as its default. (M) **`build` and `copy` in the server**, once the server can build without a Docker daemon (the item before) and `copy` has the safety backup and anonymization from Now and Next, so that the button cannot overwrite an environment without a way back. (M)

## Not planned (and why)

**Plugin and theme installs or updates from the WordPress dashboard.** A site's code is its image; changes arrive through Git, `build` and `promote`, which is what makes every environment reproducible and every change reviewable.

**Media offload to object storage in the chart.** Uploads stay on a volume. Offloading needs a plugin, URL rewriting and compatibility testing for each site, and it pays off only for a site that must run more than one pod without ReadWriteMany storage. Such a site can put an offload plugin in its image and set it in its values.

**Publishing a staging database to live as a routine step.** Code moves up through images; content stays where editors write it. Pushing a staging database to live overwrites what editors did in the meantime. `wpfleet copy --to production --yes-production` stays for a one-off such as a site's launch, but the server will not offer it and no workflow will build on it.

**Bundling Prometheus, Grafana, Loki or an uptime service.** Clusters already run monitoring; wpfleet adds probes, rules and metrics to it.

**Its own vulnerability database.** Wordfence, Patchstack and WPScan maintain the data; wpfleet matches against them.

**Logs for editors.** PHP errors can contain paths, queries and personal data, and an editor cannot act on them. Editors see a site's state in words; operators use `wpfleet logs`.

**Managing WordPress users and roles across the fleet.** The one-time admin login already maps the signed-in person to their WordPress account, and an OpenID Connect plugin in the image is the right way to centralize accounts. Offboarding is the exception, under Later.

**More than one server replica.** The server keeps its per-site locks and its pushes to the fleet repository in memory, and its chart uses the `Recreate` strategy so that two servers never run at once. A second replica would need a shared lock and a leader for the Git sync, which is a database or a coordination service the server does not otherwise need; one replica that restarts in seconds is enough for a fleet's platform team and editors.

**Resource sizing advice and cost reports.** Per-site `resources`, `replicas` and autoscaling already exist, and the Vertical Pod Autoscaler and tools such as OpenCost do recommendations and cost by label better than wpfleet could; wpfleet keeps its labels consistent so they can group by site.

**Queued publications at a set time.** A queue is state, and the server keeps none; a scheduled merge in the Git host or a CI job does it today.
