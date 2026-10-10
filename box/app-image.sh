#!/bin/sh
# EmberStorm's own image for a box, sourced by build.sh and release.sh: never
# whatever ":latest" holds at that moment - anybody able to push that tag
# would have their image baked into every box and signed into the next
# update (the box's blind security review). It is the image GitHub's own
# workflow built from one commit on main (APP_COMMIT: a commit or tag,
# default the checkout's HEAD), found by the tag it gives every build
# ("sha-<commit>"), pinned by digest, and - where it counts - checked against
# the provenance GitHub signed for that build - by that workflow, from that
# very commit, on main - so a pushed image cannot pass for one built from
# the code, nor an older or another branch's build relabelled.
#
# Sets APP_IMAGE (name@sha256:...) and APP_COMMIT. app_image strict refuses
# an image whose provenance cannot be checked; without it a missing check is
# only said.

APP_REPO=ghcr.io/gabrielhollberg/soundstorm
APP_SOURCE=GabrielHollberg/emberstorm

app_git() { git -c safe.directory='*' -C "$repo" "$@"; }

app_image() {
	strict=${1:-}
	want=${APP_COMMIT:-HEAD}
	APP_COMMIT=$(app_git rev-parse --verify --quiet "$want^{commit}") || {
		echo "No commit $want in this checkout." >&2
		return 1
	}
	# Only what is on main: CI publishes nothing else, and a branch's build
	# is not a release.
	app_git fetch -q origin main 2>/dev/null || true
	if ! app_git merge-base --is-ancestor "$APP_COMMIT" origin/main 2>/dev/null; then
		echo "Commit $APP_COMMIT is not on main (git fetch, or push it first)." >&2
		return 1
	fi
	tag="$APP_REPO:sha-$APP_COMMIT"
	# The digest is the sha256 of the manifest's bytes exactly as served (a
	# shell variable would drop a final newline and change it).
	raw=$(mktemp)
	if ! skopeo inspect --raw "docker://$tag" >"$raw"; then
		rm -f "$raw"
		echo "No image built for $APP_COMMIT ($tag): wait for GitHub's build of it to finish." >&2
		return 1
	fi
	APP_IMAGE="$APP_REPO@sha256:$(sha256sum <"$raw" | cut -d' ' -f1)"
	rm -f "$raw"
	if command -v gh >/dev/null 2>&1 &&
		gh attestation verify "oci://$APP_IMAGE" --repo "$APP_SOURCE" \
			--signer-workflow "$APP_SOURCE/.github/workflows/publish.yml" \
			--source-digest "$APP_COMMIT" --source-ref refs/heads/main >/dev/null 2>&1; then
		echo "  $APP_IMAGE: built by GitHub from $APP_COMMIT (provenance checked)"
		return 0
	fi
	if [ "$strict" = strict ]; then
		echo "The provenance of $APP_IMAGE could not be checked. Install GitHub's command line" >&2
		echo "(apt-get install gh) and sign in once (gh auth login), then run this again." >&2
		return 1
	fi
	echo "  $APP_IMAGE: from $APP_COMMIT (provenance NOT checked: install gh and sign in to check it)"
}
