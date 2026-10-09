#!/bin/sh
# Watches a GitHub pull request for the babysit-pr skill: a Djinn watcher runs it. It looks at the pull request every
# minute and prints a paragraph only when something changed: a summary line first (checks, mergeable state, review
# decision, comments), then the latest comment. It prints MERGED and exits once the pull request is merged, CLOSED
# once it is closed without a merge. A failed look (no network, gh not logged in) prints once, then it tries again.
#
# Usage: watch.sh <pr> [seconds between two looks, 60 by default]. Needs gh, logged in.
set -u
pr=${1:?usage: watch.sh <pr> [seconds]}
every=${2:-60}
last=""

say() {
	# A paragraph: printed only when it differs from the last one.
	if [ "$1" != "$last" ]; then
		printf '%s\n\n' "$1"
		last=$1
	fi
}

while :; do
	if ! view=$(gh pr view "$pr" --json state,mergeable,reviewDecision,comments,reviews --jq '
		[.state, .mergeable, (.reviewDecision // "" | if . == "" then "none" else . end),
		 ((.comments | length) + (.reviews | map(select(.body != "")) | length) | tostring),
		 ((.comments + (.reviews | map(select(.body != "")))) | sort_by(.createdAt // .submittedAt) | last
		  | if . == null then "" else "latest comment, by \(.author.login): \(.body | split("\n")[0])" end)]
		| join("\t")' 2>&1); then
		say "PR #$pr: gh failed, trying again: $(printf '%s' "$view" | head -n 1)"
		sleep "$every"
		continue
	fi
	state=$(printf '%s' "$view" | cut -f 1)
	case $state in
	MERGED)
		say "MERGED: PR #$pr is merged."
		exit 0
		;;
	CLOSED)
		say "CLOSED: PR #$pr is closed without a merge."
		exit 0
		;;
	esac
	mergeable=$(printf '%s' "$view" | cut -f 2)
	review=$(printf '%s' "$view" | cut -f 3)
	comments=$(printf '%s' "$view" | cut -f 4)
	latest=$(printf '%s' "$view" | cut -f 5-)
	# gh pr checks exits non-zero while a check fails or is pending: its output is what counts.
	checks=$(gh pr checks "$pr" --json name,bucket --jq '
		(group_by(.bucket) | map("\(length) \(.[0].bucket)") | join(", ")) as $count
		| (map(select(.bucket == "fail") | .name) | join(", ")) as $failed
		| if length == 0 then "none" elif $failed == "" then $count else "\($count) (failed: \($failed))" end' 2>/dev/null)
	summary="PR #$pr · checks: ${checks:-none} · mergeable: $mergeable · review: $review · comments: $comments"
	if [ -n "$latest" ]; then
		summary="$summary
$latest"
	fi
	say "$summary"
	sleep "$every"
done
