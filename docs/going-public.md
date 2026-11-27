# Going public

wpfleet is private until it has run on a real fleet. This is the checklist for the day it goes public, for the maintainer who does it. Do the steps in order: several cannot be undone, and each one assumes the steps before it are done. Making the repository public needs an owner of the `rahacloud` organization.

## 1. The precondition: a trial on a real fleet

Do not start before wpfleet has run on a real staging and development fleet, on a cluster that is not a local test cluster, and the trial passed. Passed means:

- `sites list`, `diff`, `deploy`, `promote` and `admin` worked on every site of both environments, and `diff` showed no change after a deploy.
- `build` pushed a site image to the fleet's real registry, and the site ran from it.
- `backup run`, `backup list` and `backup restore` worked against the real bucket, and a restored site came up with its data and uploads.
- `copy` gave one environment a working copy of the other.
- `wpfleet serve` ran from its chart with the organization's real identity provider, and a person with only an editor's groups saw only their sites and could open the admin.

Fix what the trial finds on `main` first. Then change the status line at the top of `README.md`: say what ran on a real fleet and what did not, and never name the organization that ran it. The trial's own notes stay in that organization's private repository.

## 2. The files a public repository needs

- `CODE_OF_CONDUCT.md` promises a dedicated contact address for reports. Put the address in the Enforcement section, in place of the sentence that promises it.
- `SECURITY.md` sends reporters to GitHub's private vulnerability reporting, which `hack/go-public.sh` turns on in step 6. Read it once more and check the response times are ones the maintainers can keep.
- Check that `main` is green: CI and Security on its last commit, and Release check on its last run.

## 3. The history scan

Making the repository public publishes every commit on every branch, not only what `main` has today. Run the history scan over all of it before anything else changes, and go on only when it is clean. It looks for:

- **Secrets** in any commit, with a scanner such as [gitleaks](https://github.com/gitleaks/gitleaks) over the full history (`gitleaks git --log-opts=--all`). gitleaks reads `.gitleaks.toml` at the root, which allows the two values it is known to mistake for secrets, so a clean history reports no leaks.
- **Client data** in any commit, message or file: the names, hosts, addresses, registries, namespaces and credentials of the organizations that wpfleet has been used for. That list of names lives outside this repository, and must never be committed to it.
- **Commit metadata**: the author and committer names and email addresses of every commit become public too.
- **Branches and tags**: delete every branch that is not `main` and is not meant to be public. There must be no tag yet (see step 7).

When the scan finds a secret, rotate the secret first, whatever else happens: it was in the history, so treat it as leaked. When it finds a secret or client data, rewrite the history (for example with [git filter-repo](https://github.com/newren/git-filter-repo)) and force-push before the repository becomes public. After that moment a rewrite is too late, because forks and caches keep the old commits.

### What the scan of 2026-11-27 found

The scan of 2026-11-27 covered every commit, message, file and image, and found no secret and no client data in any file. It left two decisions for the maintainer, and both must be taken before step 5.

**Decision 1, required: rewrite the history, or do not go public.** The message of commit `88aa0e1` ("Add the server's chart and image, and build the image in CI") names the registry mirror of an organization that wpfleet was used for, in the sentence "Docker Hub images also pull through the … mirror if gcr.io stops being reachable". The name is in that message only, not in any file, so a new commit cannot remove it. The rewrite replaces it with "a registry mirror", changes the hash of `88aa0e1` and of every commit after it, and needs a force-push to `main`. Do it before step 6, whose ruleset blocks force-pushes. Run it in a fresh clone, never in a working clone:

```bash
git clone git@github.com:rahacloud/wpfleet.git wpfleet-rewrite
cd wpfleet-rewrite
printf '%s\n' 'regex:pull through the \S+ mirror==>pull through a registry mirror' > ../wpfleet-messages.txt
git filter-repo --replace-message ../wpfleet-messages.txt
git log --all --format=%B | grep -c 'pull through the'   # expect 0
```

Then search the rewritten history once more for every name on the private list, and expect nothing. git filter-repo removes the `origin` remote, so add it back and push:

```bash
git remote add origin git@github.com:rahacloud/wpfleet.git
git push --force origin main
```

Afterwards, every other clone and worktree has old history: delete it and clone again, or `git fetch origin && git reset --hard origin/main` on a clean `main`. A commit hash quoted anywhere from before the rewrite stops resolving. GitHub can still serve the old commits to someone who knows their hashes until it collects them; the only pull request so far (#1) was closed before `88aa0e1`, so no pull request ref keeps them. If that matters, ask GitHub Support to purge the cached views before step 5.

**Decision 2, optional: the author email.** Every commit carries the maintainer's personal email address as author and committer, and it becomes public with the history. Keep it, or replace it with the GitHub noreply address (`<id>+<login>@users.noreply.github.com`; `gh api user --jq '"\(.id)+\(.login)"'` prints the first part) in the same rewrite. To replace it, write one mailmap line, `Name <id+login@users.noreply.github.com> <old address>`, to `../wpfleet-mailmap.txt`, and run this in place of the `git filter-repo` line above:

```bash
git filter-repo --replace-message ../wpfleet-messages.txt --mailmap ../wpfleet-mailmap.txt
```

Either way, set `user.email` in every clone to the address chosen, so new commits match.

## 4. What else becomes visible

Going public also opens what GitHub keeps beside the code. Check each of them:

- **Actions**: the logs and artifacts of every past workflow run become readable. Delete the runs that printed anything private.
- **Issues, pull requests and discussions**, with their comments and edit history.
- **Packages**: the ghcr.io packages keep their own visibility; step 8 handles them.

Repository secrets and variables stay secret.

## 5. Make the repository public

In the repository's settings, under **Danger Zone**, choose **Change visibility** and make it public, or:

```bash
gh repo edit rahacloud/wpfleet --visibility public --accept-visibility-change-consequences
```

From this moment the CodeQL and Scorecard workflows run, and zizmor uploads its findings to code scanning instead of failing the job; each of them skips or changes its behavior on a private repository.

## 6. The GitHub settings

`hack/go-public.sh` applies the settings with `gh api`. Every write sets the full desired state, and labels and rulesets are looked up by name first, so it is safe to run again. Look at what it will do, then run it:

```bash
hack/go-public.sh --dry-run
hack/go-public.sh
```

It turns on Dependabot alerts and security updates, private vulnerability reporting, secret scanning with push protection; sets the description and topics; creates the labels that the issue forms, Dependabot and `.github/release.yml` use; and creates four rulesets:

- `main` cannot be deleted or force-pushed, with no bypass.
- `main` needs the jobs of the CI and Security workflows to pass. Repository admins and organization owners bypass it, because the maintainers commit straight to `main`, and a required check refuses every direct push whose commit has not passed it yet. For everyone else, a pull request merges only once the checks pass.
- A `v*` tag cannot be moved or deleted, with no bypass: people may already have pulled what it published.
- Only repository admins and organization owners can create a `v*` tag, since pushing one publishes a release.

Organization owners are named on their own because an owner who is not a collaborator of the repository holds no repository role, so the admin role alone would not let them bypass. At the end the script reads each ruleset back and prints `current_user_can_bypass` for the person running it: it expects `always` for the two rulesets with a bypass and `never` for the two without, and exits with an error otherwise. Do not push to `main` until it passes; if it fails, fix the bypass list under **Settings > Rules**.

The script refuses to run while the repository is private: rulesets and secret scanning are not available on a private repository on the organization's plan. Then check **Settings > Rules** and **Settings > Code security** by hand, and do what the API cannot: upload a social preview image (1280 by 640 pixels, from `docs/images/banner.svg`) under **Settings > General**.

Push one commit to `main` afterwards (a small documentation fix will do). It runs CI, Security, CodeQL and Scorecard for the first time on the public repository, and the README's CI and Scorecard badges start to show a result.

## 7. The first tag

The first tag waits until the repository is public, for two reasons. cosign signs keylessly and writes every signature to the public [Rekor](https://docs.sigstore.dev/logging/overview/) transparency log, which names the repository and the release workflow, so a tag on a private repository announces it anyway. And GitHub's artifact attestations need a public repository on the organization's plan, so a release tagged before would never get build provenance.

Cut it as [releasing.md](releasing.md#cut-a-release) says: `just release-snapshot` first, then tag `v0.1.0` on `main` and push the tag. The `Release` workflow then:

1. builds `wpfleet` for Linux, macOS and Windows on amd64 and arm64 with GoReleaser, packs the archives with the README, the license and shell completions, and writes one SBOM per archive and `checksums.txt`;
2. signs `checksums.txt` with cosign keyless, which writes the signature to Rekor, and puts the bundle on the release;
3. builds and pushes the server image `ghcr.io/rahacloud/wpfleet:0.1.0` and `:latest` for linux/amd64 and linux/arm64, and signs it by its digest;
4. writes the GitHub release with notes grouped from the commit prefixes;
5. attests the archives, the SBOMs, the checksums and the image with SLSA build provenance;
6. packages the WordPress chart and the server chart, pushes them to `oci://ghcr.io/rahacloud/charts`, signs them and attests them.

Watch the run, read the release notes, and then verify the release the way a user would, with every command in [releasing.md](releasing.md#verify-a-release). If something is wrong, do not move the tag: fix it on `main` and release `v0.1.1`.

## 8. The packages on ghcr.io

A new package on ghcr.io can start out private even when the repository is public. For each of `wpfleet`, `charts/wordpress` and `charts/wpfleet`, open the package from the organization's **Packages** tab and check its visibility; where it is private, an organization owner makes it public under **Package settings > Danger Zone**. Check that each package is linked to `rahacloud/wpfleet`, so it shows on the repository page.

Then pull everything without a login, as a stranger would:

```bash
docker logout ghcr.io
docker pull ghcr.io/rahacloud/wpfleet:0.1.0
helm registry logout ghcr.io
helm pull oci://ghcr.io/rahacloud/charts/wordpress --version 0.1.0
helm pull oci://ghcr.io/rahacloud/charts/wpfleet --version 0.1.0
```

## 9. pkg.go.dev and Go Report Card

Ask the Go module proxy for the release, which makes [pkg.go.dev](https://pkg.go.dev/github.com/rahacloud/wpfleet) fetch it within a few minutes, and check that `go install` works from the proxy:

```bash
GOPROXY=https://proxy.golang.org go list -m github.com/rahacloud/wpfleet@v0.1.0
go install github.com/rahacloud/wpfleet/cmd/wpfleet@v0.1.0
```

If pkg.go.dev still shows nothing, open the page and choose **Request "github.com/rahacloud/wpfleet"**. Then open [goreportcard.com/report/github.com/rahacloud/wpfleet](https://goreportcard.com/report/github.com/rahacloud/wpfleet) and choose **Refresh** to make the first report. Every badge at the top of the README now shows a value; fix any that does not.

## 10. The announcement

Announce it only after every step above is done, so that the first people who arrive find a release they can verify and badges that work. Say what wpfleet does, that it is v0.1 and what that means, link the README and [getting-started.md](getting-started.md), and point to [ROADMAP.md](ROADMAP.md) for what comes next. Never name an organization that has used wpfleet without its written agreement. The announcement goes out under RahaCloud's name, so it goes through RahaCloud's review for official documents first.

## 11. Afterwards

Remove what only made sense while the repository was private, in one commit:

- the section "Before the repository is public" in [releasing.md](releasing.md), and its link to this file;
- the `if: ${{ !github.event.repository.private }}` conditions in the workflows, which now always pass;
- in `CLAUDE.md`, "The repository will be public" becomes "The repository is public"; the rule under it stays.

Keep this file: it records how the repository was opened and which settings it has.
