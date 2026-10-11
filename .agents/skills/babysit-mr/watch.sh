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
mrs=$(printf '%s' "$mr" | tr ',' ' ')
every=${2:-60}
# glab's error output, apart: a notice there must not mix with its JSON.
errors=$(mktemp)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$errors" "$tmp_dir"' EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

say() {
	curr_mr=$1
	text=$2
	last_file=$tmp_dir/last-$curr_mr
	last=""
	[ -f "$last_file" ] && last=$(cat "$last_file")
	# A paragraph: printed only when it differs from the last one.
	if [ "$text" != "$last" ]; then
		printf '%s\n\n' "$text"
		printf '%s' "$text" >"$last_file"
	fi
}

look_mr() {
	curr_mr=$1
	if ! view=$(glab mr view "$curr_mr" --comments --output json --jq '
		[(.Discussions // [])[].notes[] | select(.system | not)] as $notes
		| [.state, (.detailed_merge_status // "unknown"), (.head_pipeline.id // "" | tostring),
		   ($notes | length | tostring),
		   ($notes | sort_by(.created_at) | last
		    | if . == null then "" else "latest comment, by \(.author.username): \(.body | split("\n")[0])" end)]
		| join("\t")' 2>"$errors"); then
		say "$curr_mr" "MR !$curr_mr: glab failed, trying again: $(head -n 1 "$errors")"
		return 0
	fi
	state=$(printf '%s' "$view" | cut -f 1)
	case $state in
	merged)
		say "$curr_mr" "MERGED: MR !$curr_mr is merged."
		return 1
		;;
	closed)
		say "$curr_mr" "CLOSED: MR !$curr_mr is closed without a merge."
		return 1
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
	summary="MR !$curr_mr · pipeline: ${jobs:-none} · merge status: $merge · comments: $comments"
	if [ -n "$latest" ]; then
		summary="$summary
$latest"
	fi
	say "$curr_mr" "$summary"
	return 0
}

finished_mrs=""
while :; do
	if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		if [ -n "$(git for-each-ref --format='%(refname:short)' refs/heads/djinn/ refs/remotes/*/djinn/ 2>/dev/null)" ]; then
			azima_mrs=$(glab mr list --state opened --output json --jq '.[] | select(.source_branch | startswith("djinn/")) | .iid' 2>/dev/null || :)
			for m in $azima_mrs; do
				case " $mrs $finished_mrs " in
				*" $m "*) ;;
				*) mrs="${mrs:+$mrs }$m" ;;
				esac
			done
		fi
	fi

	active_mrs=""
	for m in $mrs; do
		if look_mr "$m"; then
			active_mrs="${active_mrs:+$active_mrs }$m"
		else
			finished_mrs="${finished_mrs:+$finished_mrs }$m"
		fi
	done
	mrs=$active_mrs
	if [ -z "$mrs" ]; then
		exit 0
	fi
	sleep "$every"
done
