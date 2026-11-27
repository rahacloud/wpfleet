#!/usr/bin/env bash
# Applies the GitHub settings that wpfleet needs once it is public: security
# features, labels, repository settings and rulesets. Run it once after
# the repository is made public. Every write sets the full desired state (labels and rulesets
# are looked up by name first), so running it again changes nothing. At the
# end it reads each ruleset back and checks who may bypass it.
#
# Usage: hack/go-public.sh [--dry-run] [--allow-private] [--repo OWNER/NAME]
#
#   --dry-run        print every gh api call that changes something instead of
#                    running it; calls that only read still run when they can
#   --allow-private  run although the repository is still private; on the
#                    free plan, rulesets and secret scanning answer 403 for a
#                    private repository, so expect those steps to fail
#   --repo           the repository, rahacloud/wpfleet by default
#
# It needs gh, logged in as an administrator of the repository, and jq.

set -euo pipefail

repo="rahacloud/wpfleet"
dry_run=false
allow_private=false

usage() {
	sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
}

while (($# > 0)); do
	case "$1" in
	--dry-run) dry_run=true ;;
	--allow-private) allow_private=true ;;
	--repo)
		if (($# < 2)); then
			echo "go-public: --repo needs OWNER/NAME" >&2
			exit 2
		fi
		repo="$2"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "go-public: unknown argument: $1" >&2
		usage >&2
		exit 2
		;;
	esac
	shift
done

for tool in gh jq; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "go-public: $tool is not on the PATH" >&2
		exit 1
	fi
done

log() {
	printf '==> %s\n' "$*" >&2
}

# read_api PATH... runs a GET, which changes nothing, in a dry run too. In a
# dry run a failed read is reported and treated as "nothing there", so the
# dry run still shows every write it would make.
read_api() {
	if "$dry_run"; then
		gh api "$@" 2>/dev/null || {
			echo "# could not read $1; assuming it does not exist" >&2
			return 1
		}
	else
		gh api "$@"
	fi
}

# write_api METHOD PATH [JSON] makes a change, or prints it in a dry run.
write_api() {
	local method="$1" path="$2" body="${3-}"
	if "$dry_run"; then
		if [[ -n "$body" ]]; then
			printf 'gh api --method %s %s --input - <<'"'"'JSON'"'"'\n%s\nJSON\n' "$method" "$path" "$(jq . <<<"$body")"
		else
			printf 'gh api --method %s %s\n' "$method" "$path"
		fi
		return 0
	fi
	if [[ -n "$body" ]]; then
		gh api --method "$method" "$path" --input - <<<"$body" >/dev/null
	else
		gh api --method "$method" "$path" >/dev/null
	fi
}

# --- The repository must be public ------------------------------------------

if private="$(read_api "repos/$repo" --jq .private)"; then
	if [[ "$private" == "true" ]]; then
		if "$dry_run"; then
			log "$repo is private; a run without --dry-run refuses until it is public"
		elif ! "$allow_private"; then
			echo "go-public: $repo is still private. Make it public first, or pass --allow-private." >&2
			exit 1
		fi
	fi
elif ! "$dry_run"; then
	echo "go-public: cannot read $repo; is gh logged in as one of its administrators?" >&2
	exit 1
fi

# --- Security features -------------------------------------------------------

log "Dependabot alerts and security updates"
write_api PUT "repos/$repo/vulnerability-alerts"
write_api PUT "repos/$repo/automated-security-fixes"

log "Private vulnerability reporting, which SECURITY.md sends reporters to"
write_api PUT "repos/$repo/private-vulnerability-reporting"

log "Secret scanning and push protection"
write_api PATCH "repos/$repo" '{
  "security_and_analysis": {
    "secret_scanning": {"status": "enabled"},
    "secret_scanning_push_protection": {"status": "enabled"}
  }
}'

# --- Repository settings -----------------------------------------------------

log "Description, features and topics"
write_api PATCH "repos/$repo" '{
  "description": "Manage a fleet of WordPress sites on Kubernetes from one Git repository: one shared Helm chart, immutable site images, one-time admin links, S3 backups and a web UI with single sign-on.",
  "has_wiki": false,
  "has_projects": false,
  "delete_branch_on_merge": true
}'
write_api PUT "repos/$repo/topics" '{
  "names": ["wordpress", "kubernetes", "helm", "gitops", "cli", "go", "fleet-management", "devops"]
}'

# --- Labels ------------------------------------------------------------------
#
# The issue forms set bug and enhancement; .github/release.yml groups pull
# requests by breaking-change, security, enhancement, bug, documentation and
# dependencies and leaves out ignore-for-release, duplicate, invalid and
# wontfix; Dependabot sets dependencies plus go, github_actions or docker.
#
# name|color|description
labels=(
	"bug|d73a4a|Something does not work as docs/design.md says it should"
	"enhancement|a2eeef|A new command, flag or behaviour"
	"breaking-change|b60205|Breaks a command, a flag, wpfleet.yaml or a chart value"
	"security|ee0701|Fixes a vulnerability or hardens wpfleet"
	"documentation|0075ca|Changes only the documentation"
	"dependencies|0366d6|Updates a dependency"
	"go|00add8|Updates a Go module"
	"github_actions|000000|Updates a GitHub Action"
	"docker|2496ed|Updates a base image"
	"ignore-for-release|ededed|Left out of the release notes"
	"duplicate|cfd3d7|Another issue or pull request already covers this"
	"invalid|e4e669|Not a problem in wpfleet"
	"wontfix|ffffff|Will not be worked on"
)

log "Labels"
for entry in "${labels[@]}"; do
	IFS='|' read -r name color description <<<"$entry"
	body="$(jq -nc --arg name "$name" --arg color "$color" --arg description "$description" \
		'{name: $name, color: $color, description: $description}')"
	encoded="$(jq -rn --arg name "$name" '$name | @uri')"
	# A label that does not exist answers 404, which is not an error here.
	if read_api "repos/$repo/labels/$encoded" >/dev/null 2>&1; then
		write_api PATCH "repos/$repo/labels/$encoded" "$(jq -c 'del(.name)' <<<"$body")"
	else
		write_api POST "repos/$repo/labels" "$body"
	fi
done

# --- Rulesets ----------------------------------------------------------------
#
# Work goes straight to main, so no ruleset asks for a pull request. The ID of
# the repository's admin role, for bypass_actors, is 5. An organization owner
# who is not a collaborator of the repository holds no repository role, so the
# rulesets that admins bypass name OrganizationAdmin as well (its actor_id is
# 1 by convention; GitHub ignores it). The GitHub Actions app, which reports
# the checks, is integration 15368.

# ruleset_id NAME prints the ID of the repository ruleset NAME, or nothing.
ruleset_id() {
	local existing
	if existing="$(read_api "repos/$repo/rulesets?includes_parents=false" --paginate)"; then
		jq -rs --arg name "$1" '[.[][] | select(.name == $name and .source_type == "Repository") | .id] | first // empty' <<<"$existing"
	fi
}

# ruleset NAME JSON creates the repository ruleset NAME, or replaces the one
# that already has that name.
ruleset() {
	local name="$1" body="$2" id
	id="$(ruleset_id "$name")"
	if [[ -n "$id" ]]; then
		write_api PUT "repos/$repo/rulesets/$id" "$body"
	else
		write_api POST "repos/$repo/rulesets" "$body"
	fi
}

log "Ruleset: main keeps its history"
ruleset "main: no force push or deletion" '{
  "name": "main: no force push or deletion",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"}
  ]
}'

# A required check stops a push whose commit has not passed it yet, which is
# every direct push to main. Admins and owners bypass this ruleset so the maintainers can
# keep committing straight to main; it holds for everyone else, and a pull
# request merges only once these jobs pass. The contexts are the job names in
# .github/workflows/ci.yml (CI) and security.yml (Security); rename a job and
# this list must follow.
log "Ruleset: CI and Security pass on main"
ruleset "main: CI and Security pass" '{
  "name": "main: CI and Security pass",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [
    {"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"},
    {"actor_id": 1, "actor_type": "OrganizationAdmin", "bypass_mode": "always"}
  ],
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": false,
        "do_not_enforce_on_create": false,
        "required_status_checks": [
          {"context": "golangci-lint", "integration_id": 15368},
          {"context": "helm lint", "integration_id": 15368},
          {"context": "build & test", "integration_id": 15368},
          {"context": "image", "integration_id": 15368},
          {"context": "govulncheck", "integration_id": 15368},
          {"context": "actionlint", "integration_id": 15368},
          {"context": "zizmor", "integration_id": 15368}
        ]
      }
    }
  ]
}'

# A release tag never moves and never goes away: people may already have
# pulled what it published (docs/releasing.md). Nobody bypasses this one.
log "Ruleset: release tags are immutable"
ruleset "tags v*: no update or deletion" '{
  "name": "tags v*: no update or deletion",
  "target": "tag",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "update", "parameters": {"update_allows_fetch_and_merge": false}}
  ]
}'

# Pushing a v* tag publishes a release, so only admins and owners may create
# one.
log "Ruleset: only admins create release tags"
ruleset "tags v*: admins create" '{
  "name": "tags v*: admins create",
  "target": "tag",
  "enforcement": "active",
  "bypass_actors": [
    {"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"},
    {"actor_id": 1, "actor_type": "OrganizationAdmin", "bypass_mode": "always"}
  ],
  "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
  "rules": [
    {"type": "creation"}
  ]
}'

# --- Check the rulesets -------------------------------------------------------
#
# The maintainer who runs this must still be able to push to main and push a
# release tag, and nobody may bypass the two rulesets without bypass actors.
# GitHub answers current_user_can_bypass for the user gh is logged in as.

if "$dry_run"; then
	log "Dry run: nothing was changed"
	exit 0
fi

log "Who may bypass each ruleset, as $(gh api user --jq .login)"
# name|expected current_user_can_bypass
expected=(
	"main: no force push or deletion|never"
	"main: CI and Security pass|always"
	"tags v*: no update or deletion|never"
	"tags v*: admins create|always"
)
failed=false
for entry in "${expected[@]}"; do
	IFS='|' read -r name want <<<"$entry"
	id="$(ruleset_id "$name")"
	got="missing"
	if [[ -n "$id" ]]; then
		got="$(gh api "repos/$repo/rulesets/$id" --jq '.current_user_can_bypass // "unknown"')"
	fi
	printf '%-34s %-8s (want %s)\n' "$name" "$got" "$want" >&2
	if [[ "$got" != "$want" ]]; then
		failed=true
	fi
done
if "$failed"; then
	echo "go-public: a ruleset's bypass is not what it should be; fix it under Settings > Rules before pushing to main" >&2
	exit 1
fi

log "Done. Check Settings > Rules and Settings > Code security of $repo."
