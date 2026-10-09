#!/bin/sh
# Watches a GitLab merge request for the babysit-mr skill: a Djinn watcher runs it. It looks at the merge request
# every minute and prints a paragraph only when something changed: a summary line first (its pipeline's jobs, merge
# status, comments), then the latest comment. It prints MERGED and exits once the merge request is merged, CLOSED
# once it is closed without a merge. A failed look (no network, glab not logged in) prints once, then it tries again.
#
# Usage: watch.sh <mr> [seconds between two looks, 60 by default]. Needs glab 1.100 or later (its --jq), logged in,
# run in a clone of the merge request's project.
set -u
mr=${1:?usage: watch.sh <mr> [seconds]}
every=${2:-60}
last=""
# glab's error output, apart: a notice there must not mix with its JSON.
errors=$(mktemp)
trap 'rm -f "$errors"' EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

say() {
	# A paragraph: printed only when it differs from the last one.
	if [ "$1" != "$last" ]; then
		printf '%s\n\n' "$1"
		last=$1
	fi
}

while :; do
	if ! view=$(glab mr view "$mr" --comments --output json --jq '
		[(.Discussions // [])[].notes[] | select(.system | not)] as $notes
		| [.state, (.detailed_merge_status // "unknown"), (.head_pipeline.id // "" | tostring),
		   ($notes | length | tostring),
		   ($notes | sort_by(.created_at) | last
		    | if . == null then "" else "latest comment, by \(.author.username): \(.body | split("\n")[0])" end)]
		| join("\t")' 2>"$errors"); then
		say "MR !$mr: glab failed, trying again: $(head -n 1 "$errors")"
		sleep "$every"
		continue
	fi
	state=$(printf '%s' "$view" | cut -f 1)
	case $state in
	merged)
		say "MERGED: MR !$mr is merged."
		exit 0
		;;
	closed)
		say "CLOSED: MR !$mr is closed without a merge."
		exit 0
		;;
	esac
	merge=$(printf '%s' "$view" | cut -f 2)
	pipeline=$(printf '%s' "$view" | cut -f 3)
	comments=$(printf '%s' "$view" | cut -f 4)
	latest=$(printf '%s' "$view" | cut -f 5-)
	# The jobs of the merge request's latest pipeline; a failure allowed to fail counts apart, as a warning.
	jobs=""
	if [ -n "$pipeline" ]; then
		jobs=$(glab ci get --pipeline-id "$pipeline" --output json --jq '
			(.jobs // []) | map(if .status == "failed" and .allow_failure then .status = "warning" else . end)
			| (group_by(.status) | map("\(length) \(.[0].status)") | join(", ")) as $count
			| (map(select(.status == "failed") | .name) | join(", ")) as $failed
			| if length == 0 then "" elif $failed == "" then $count else "\($count) (failed: \($failed))" end' 2>/dev/null)
	fi
	summary="MR !$mr · pipeline: ${jobs:-none} · merge status: $merge · comments: $comments"
	if [ -n "$latest" ]; then
		summary="$summary
$latest"
	fi
	say "$summary"
	sleep "$every"
done
