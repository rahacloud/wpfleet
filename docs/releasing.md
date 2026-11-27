# Releasing wpfleet

A release starts when someone pushes a tag `v*`. The `Release` workflow (`.github/workflows/release.yml`) builds everything from that tag with [GoReleaser](https://goreleaser.com) and Helm, signs it with [cosign](https://docs.sigstore.dev/cosign/) and records where it came from. Nobody builds or uploads a release by hand.

The first release is part of making the repository public: follow [going-public.md](going-public.md) for it, which comes back to the steps below at the right moment.

## Cut a release

1. Check that `main` is green and that `docs/design.md` and the README describe what the release does.
2. Pick the version by [Semantic Versioning](https://semver.org). Before 1.0, a change that breaks a command, a flag, `wpfleet.yaml` or a chart value raises the minor version. A tag with a suffix, such as `v0.2.0-rc.1`, makes a pre-release: the GitHub release is marked as one and the image gets no `latest` tag.
3. Try the release locally. `just release-snapshot` runs the whole release into `dist/` without publishing or signing anything; it needs Docker with Buildx and [syft](https://github.com/anchore/syft) on the `PATH`.
4. Tag the commit on `main` and push the tag:

   ```bash
   git tag -a v0.1.0 -m "wpfleet v0.1.0"
   git push origin v0.1.0
   ```

5. Watch the workflow run. When it is done, read the GitHub release: its notes come from the commits since the previous tag.

The release notes group commits by their prefix: `feat:` under Features, `fix:` under Bug fixes, `sec:` under Security, `perf:` under Performance, `build(deps):`, `chore(deps):` and `ci(deps):` under Dependencies, and every other commit under Other changes. Commits that start with `docs:`, `test:`, `chore:` or `ci:` are left out. A commit message that starts with a plain sentence is fine; it lands in Other changes.

A broken release is not fixed by moving its tag, because people may already have pulled what it published. Fix the problem on `main` and release the next patch version.

## What a release publishes

| What | Where | Signed | Provenance |
| --- | --- | --- | --- |
| `wpfleet` for Linux, macOS and Windows on amd64 and arm64, as `.tar.gz` (`.zip` for Windows) with the README, the license and shell completions | the GitHub release | through `checksums.txt` | yes |
| One SPDX SBOM per archive (`<archive>.sbom.json`), made by syft | the GitHub release | through `checksums.txt` | yes |
| `checksums.txt` and its cosign bundle `checksums.txt.sigstore.json` | the GitHub release | yes | yes |
| The server image for linux/amd64 and linux/arm64, with an SBOM attached by BuildKit | `ghcr.io/rahacloud/wpfleet:<version>`, and `:latest` for a release that is not a pre-release | yes | yes |
| The WordPress chart | `oci://ghcr.io/rahacloud/charts/wordpress`, version `<version>` | yes | yes |
| The server chart | `oci://ghcr.io/rahacloud/charts/wpfleet`, version `<version>` | yes | yes |

`<version>` is the tag without its `v`: tag `v0.1.0` publishes image `0.1.0` and charts `0.1.0`. The WordPress chart keeps its `appVersion`, which is the WordPress version it runs by default. The server chart's `appVersion` is the release, so the chart runs the image of the same tag unless `image.tag` says otherwise.

The binaries are reproducible: `-trimpath`, no cgo, and every timestamp in the binaries and archives is the commit's. Building the same tag again with the same Go version gives the same archives and the same lines for them in `checksums.txt`. The SBOMs are not reproducible, because syft writes the time and a random document namespace into each one.

## Verify a release

Every signature is keyless. cosign gets a short-lived certificate from [Sigstore](https://www.sigstore.dev)'s Fulcio for the release workflow's GitHub OIDC identity and logs the signature in the public Rekor transparency log, so there is no key to keep or leak. To verify, you check that identity: the workflow file and the tag it ran for.

The examples verify `v0.1.0`; replace the version in every command.

### The binaries

Download the archive you want, `checksums.txt` and `checksums.txt.sigstore.json` from the GitHub release into one directory. Verify the signature of the checksums first, then the archive against them:

```bash
cosign verify-blob \
  --certificate-identity 'https://github.com/rahacloud/wpfleet/.github/workflows/release.yml@refs/tags/v0.1.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --bundle checksums.txt.sigstore.json \
  checksums.txt

sha256sum --check --ignore-missing checksums.txt
```

On macOS, `shasum -a 256 --check --ignore-missing checksums.txt` does the same as `sha256sum`.

### The image

```bash
cosign verify ghcr.io/rahacloud/wpfleet:0.1.0 \
  --certificate-identity 'https://github.com/rahacloud/wpfleet/.github/workflows/release.yml@refs/tags/v0.1.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

cosign resolves the tag to a digest and checks the signature made on that digest. Deploy the image by the digest it prints, not by the tag. The image's SBOM is an attestation in the registry:

```bash
docker buildx imagetools inspect ghcr.io/rahacloud/wpfleet:0.1.0 --format '{{ json .SBOM }}'
```

### The charts

The charts are OCI artifacts, so cosign verifies them like an image:

```bash
cosign verify ghcr.io/rahacloud/charts/wordpress:0.1.0 \
  --certificate-identity 'https://github.com/rahacloud/wpfleet/.github/workflows/release.yml@refs/tags/v0.1.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

helm install wpfleet oci://ghcr.io/rahacloud/charts/wpfleet --version 0.1.0
```

### Provenance

Each artifact also has a [SLSA build provenance](https://slsa.dev/spec/v1.0/provenance) attestation, made by GitHub's artifact attestations. It says which repository, commit, workflow and run built the artifact. Verify it with the GitHub CLI:

```bash
gh attestation verify wpfleet_0.1.0_linux_amd64.tar.gz \
  --repo rahacloud/wpfleet \
  --signer-workflow rahacloud/wpfleet/.github/workflows/release.yml

gh attestation verify oci://ghcr.io/rahacloud/wpfleet:0.1.0 --repo rahacloud/wpfleet
gh attestation verify oci://ghcr.io/rahacloud/charts/wordpress:0.1.0 --repo rahacloud/wpfleet --bundle-from-oci
```

`gh attestation verify` needs a GitHub login. Without `--bundle-from-oci` it fetches the attestation from the GitHub API. The workflow also pushes each chart's attestation to the registry beside the chart, and `--bundle-from-oci` reads it from there; the chart command above uses it, and it also works without the flag. The archives and the image have their attestations only in the GitHub API, so their commands do not take the flag.

## Before the repository is public

wpfleet stays private until it has run on a real fleet, and the first tag waits until the repository is public. [going-public.md](going-public.md) is the ordered checklist for that day, from the history scan to the announcement. A tag pushed while the repository is still private would publish a release that differs in three ways:

- GitHub artifact attestations need GitHub Enterprise Cloud on a private repository, so the workflow skips them, and that release never gets provenance.
- The cosign signatures still go to the public Rekor log, which names the repository and the workflow for anyone who searches it, though not what was built.
- New packages on ghcr.io are private. An organization owner makes `wpfleet`, `charts/wordpress` and `charts/wpfleet` public in the package settings once, after their first push.
