#!/bin/sh
# Watches a GitHub pull request for the babysit-pr skill: a Djinn watcher runs it, and each paragraph it prints wakes
# the wish's lead. So it looks at the pull request every minute and speaks only of what needs the lead:
#   - a check that newly fails, by name: once per failure, again only after a new push or a re-run;
#   - all checks passing, once per push, and again after a failure: the lead waits for it before merging;
#   - a new comment, or a review with a body: the latest one;
#   - a review decision that turns to CHANGES_REQUESTED;
#   - the pull request no longer mergeable (CONFLICTING; UNKNOWN is GitHub still computing, and says nothing);
#   - MERGED, then it exits; CLOSED, without a merge, then it exits.
# Pending checks and partial passes say nothing. A failed look (no network, gh not logged in) prints once, then it
# tries again. What it said is kept outside the repository, under $DJINN_HOME by the watcher's task
# ($DJINN_TASK_ID), so that a watcher Djinn starts again does not say it again; run by hand, it keeps it while it runs.
#
# Usage: watch.sh <pr> [seconds between two looks, 60 by default] [looks before it exits, endless by default].
# Needs gh, logged in. POSIX sh, for Linux and macOS.
set -u
pr=${1:?usage: watch.sh <pr> [seconds] [looks]}
every=${2:-60}
looks=${3:-0}
tab=$(printf '\t')
us=$(printf '\037')
nl='
'

# gh's error output, apart: a notice there must not mix with its JSON.
errors=$(mktemp)
if [ -n "${DJINN_TASK_ID:-}" ] && [ -n "${DJINN_HOME:-}" ]; then
	state=$DJINN_HOME/watchers/$DJINN_TASK_ID/babysit-pr-$pr
	mkdir -p "$DJINN_HOME/watchers/$DJINN_TASK_ID"
	trap 'rm -f "$errors"' EXIT
else
	state=$(mktemp)
	trap 'rm -f "$errors" "$state"' EXIT
fi
trap 'exit 143' TERM
trap 'exit 130' INT

# What it already said: the head commit it saw, the failed checks it named for it (one per line), whether it said
# they all pass, the latest comment it showed (its time and author), the last review decision and mergeable state,
# and whether it said gh failed.
head="" failed="" green="" comment="" review="" mergeable="" broken=""
if [ -f "$state" ]; then
	while IFS= read -r line; do
		value=${line#*"$tab"}
		case $line in
		"head$tab"*) head=$value ;;
		"failed$tab"*) failed=${failed:+$failed$nl}$value ;;
		"green$tab"*) green=$value ;;
		"comment$tab"*) comment=$value ;;
		"review$tab"*) review=$value ;;
		"mergeable$tab"*) mergeable=$value ;;
		"broken$tab"*) broken=$value ;;
		esac
	done <"$state"
fi
saved=""

save() {
	data="head$tab$head${nl}green$tab$green${nl}comment$tab$comment${nl}review$tab$review${nl}mergeable$tab$mergeable"
	data="$data${nl}broken$tab$broken$nl"
	while IFS= read -r name; do
		[ -n "$name" ] && data="${data}failed$tab$name$nl"
	done <<EOF
$failed
EOF
	if [ "$data" != "$saved" ]; then
		printf '%s' "$data" >"$state.new" && mv "$state.new" "$state" && saved=$data
	fi
}

look() {
	if ! view=$(gh pr view "$pr" --json state,mergeable,reviewDecision,headRefOid,comments,reviews --jq '
		((.comments + (.reviews | map(select(.body != "")))) | sort_by(.createdAt // .submittedAt) | last) as $latest
		| [.state, .mergeable, (.reviewDecision // ""), .headRefOid,
		   (if $latest == null then "" else "\($latest.createdAt // $latest.submittedAt) \($latest.author.login)" end),
		   (if $latest == null then "" else "\($latest.author.login): \($latest.body | split("\n")[0])" end)]
		| join("\u001f")' 2>"$errors"); then
		if [ "$broken" != yes ]; then
			IFS= read -r why <"$errors" || :
			printf '%s\n\n' "PR #$pr: gh failed, trying again: ${why:-}"
			broken=yes
			save
		fi
		return
	fi
	broken=""
	set -f
	old=$IFS
	IFS=$us
	# The fields, split on the unit separator only, without globbing.
	set -- $view
	IFS=$old
	set +f
	case ${1:-} in
	MERGED)
		printf '%s\n\n' "MERGED: PR #$pr is merged."
		exit 0
		;;
	CLOSED)
		printf '%s\n\n' "CLOSED: PR #$pr is closed without a merge."
		exit 0
		;;
	esac
	now_mergeable=${2:-} now_review=${3:-} now_head=${4:-} latest=${5:-} text=${6:-}
	news="" more=""
	if [ "$now_head" != "$head" ]; then
		# A new push: its checks start over.
		head=$now_head failed="" green=""
	fi

	# The checks: their number, whether they all pass (or skip), then the names of those that fail. gh pr checks
	# exits non-zero while a check fails or is pending: its output is what counts, and none says nothing.
	checks=$(gh pr checks "$pr" --json name,bucket --jq '
		length, all(.bucket == "pass" or .bucket == "skipping"), (map(select(.bucket == "fail") | .name) | unique[])' \
		2>/dev/null)
	if [ -n "$checks" ]; then
		total=${checks%%"$nl"*}
		rest=${checks#*"$nl"}
		all=${rest%%"$nl"*}
		case $rest in
		*"$nl"*) now_failed=${rest#*"$nl"} ;;
		*) now_failed="" ;;
		esac
		new=""
		while IFS= read -r name; do
			case "$nl$failed$nl" in
			*"$nl$name$nl"*) ;;
			*) [ -n "$name" ] && new=${new:+$new, }$name ;;
			esac
		done <<EOF
$now_failed
EOF
		if [ -n "$new" ]; then
			news="$news · checks failed: $new"
			green=""
		fi
		# A check that runs again leaves the list: failing again, it is new.
		failed=$now_failed
		if [ "$all" = true ] && [ "$total" -gt 0 ] && [ "$green" != yes ]; then
			news="$news · all checks pass ($total)"
			green=yes
		fi
	fi

	if [ "$now_review" != "$review" ]; then
		[ "$now_review" = CHANGES_REQUESTED ] && news="$news · changes requested"
		review=$now_review
	fi
	case $now_mergeable in
	UNKNOWN | "$mergeable") ;;
	*)
		[ "$now_mergeable" != MERGEABLE ] && news="$news · mergeable: $now_mergeable"
		mergeable=$now_mergeable
		;;
	esac
	# A comment newer than the last one shown: its time first, so that the latest sorts last.
	if [ -n "$latest" ] && [ "$latest" != "$comment" ] && awk -v a="$latest" -v b="$comment" 'BEGIN { exit !(a > b) }'; then
		news="$news · new comment by ${text%%:*}"
		more="${nl}latest comment, by $text"
		comment=$latest
	fi

	if [ -n "$news" ]; then
		printf '%s\n\n' "PR #$pr$news$more"
	fi
	save
}

n=0
while :; do
	look
	n=$((n + 1))
	if [ "$looks" -gt 0 ] && [ "$n" -ge "$looks" ]; then
		exit 0
	fi
	sleep "$every"
done
