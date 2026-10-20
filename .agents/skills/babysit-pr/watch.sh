#!/bin/sh
# Watches a GitHub pull request for the babysit-pr skill: a Djinn watcher runs it, and each paragraph it prints wakes
# the wish's lead. So it looks at the pull request every minute and speaks only of what needs the lead:
#   - a check that newly fails, by name, at the head commit, with its job's link: once per failure, again only after
#     a new push or a re-run;
#   - all checks passing, at the head commit, once per push, and again after a failure: the lead waits for it before
#     merging;
#   - each new comment, one paragraph each, with its author, its link and its first line: a comment of the
#     conversation, a review (approved, changes requested, or commented with a body), a comment on a line of the diff;
#     never twice the same, known by its link. Its first look says only the latest one there;
#   - a review decision that turns to CHANGES_REQUESTED;
#   - the pull request no longer mergeable (CONFLICTING; UNKNOWN is GitHub still computing, and says nothing);
#   - MERGED, then it exits; CLOSED, without a merge, then it exits.
# Pending checks and partial passes say nothing. A failed look (no network, gh not logged in) prints once, then it
# tries again. What it said is kept outside the repository, under $DJINN_HOME by the watcher's task
# ($DJINN_TASK_ID), so that a watcher Djinn starts again does not say it again; run by hand, it keeps it while it runs.
# Each paragraph names what it is about (the head commit, the failed job), so that Djinn, which tells the lead a
# paragraph only when it differs from the last one, never takes a new failure for the old one. Only the checks are
# said again on its first look: a paragraph printed as a Djinn hung or was killed reached no lead, though it is kept
# as said (PR #2, 12/10/2026); Djinn tells the lead nothing when it is the task's last line.
#
# It speaks only of the pull requests it is given, never of another open one: an azima's (a djinn/ branch) has its
# own watcher, and a watcher that read them all spoke of a pull request it was not given (W8: watch.sh 6 told PR #7).
#
# Usage: watch.sh <pr>[,<pr>...] [seconds between two looks, 60 by default] [looks before it exits, endless by
# default]. With several, it exits once all are merged or closed.
# Needs gh, logged in. POSIX sh, for Linux and macOS.
set -u
pr=${1:?usage: watch.sh <pr> [seconds] [looks]}
prs=$(printf '%s' "$pr" | tr ',' ' ')
every=${2:-60}
looks=${3:-0}
# The comments of the pull request: of its conversation, its reviews, and theirs on lines of the diff.
query='query($owner: String!, $repo: String!, $pr: Int!) {
	repository(owner: $owner, name: $repo) {
		pullRequest(number: $pr) {
			comments(last: 100) { nodes { url createdAt body author { login } } }
			reviews(last: 100) { nodes { url state submittedAt body author { login }
				comments(first: 100) { nodes { url body path line originalLine author { login } } } } }
		}
	}
}'
notes_jq='def who: .author.login // "ghost";
	def first_line: .body | gsub("\r"; "") | [splits("\n")]
		| map(select(test("\\S") and (test("^\\s*```") | not))) | (first // "") | .[0:120];
	.data.repository.pullRequest as $pr
	| [($pr.comments.nodes[] | {at: .createdAt, url, what: "new comment by \(who)", text: first_line}),
	   ($pr.reviews.nodes[] | select(.state != "PENDING")
	    | (select(.body != "" or .state == "APPROVED" or .state == "CHANGES_REQUESTED")
	       | {at: .submittedAt, url, text: first_line,
	          what: "\({APPROVED: "approved", CHANGES_REQUESTED: "changes requested"}[.state] // "review") by \(who)"}),
	      (.submittedAt as $at | .comments.nodes[]
	       | {at: $at, url, text: first_line,
	          what: "new comment by \(who) on \(.path)\(.line // .originalLine | if . then ":\(.)" else "" end)"}))]
	| sort_by(.at)[]
	| [.url, .what, .text] | join("\u001f")'
tab=$(printf '\t')
us=$(printf '\037')
nl='
'

# gh's error output, apart: a notice there must not mix with its JSON.
errors=$(mktemp)
tmp_dir=""
if [ -n "${DJINN_TASK_ID:-}" ] && [ -n "${DJINN_HOME:-}" ]; then
	state_dir=$DJINN_HOME/watchers/$DJINN_TASK_ID
	mkdir -p "$state_dir"
	trap 'rm -f "$errors"' EXIT
else
	tmp_dir=$(mktemp -d)
	state_dir=$tmp_dir
	trap 'rm -rf "$errors" "$tmp_dir"' EXIT
fi
trap 'exit 143' TERM
trap 'exit 130' INT

# What it already said: the head commit it saw, the failed checks it named for it (one per line), whether it said
# they all pass, the comments it read (their links, one per line) and whether it read them once, the last review
# decision and mergeable state, and whether it said gh failed.
head="" failed="" green="" seen="" notes="" review="" mergeable="" broken="" saved=""
# The pull requests it read since it started: the first look that reads one says its checks again.
looked=""

load_state() {
	curr=$1
	state=$state_dir/babysit-pr-$curr
	head="" failed="" green="" seen="" notes="" review="" mergeable="" broken="" saved=""
	if [ -f "$state" ]; then
		while IFS= read -r line; do
			value=${line#*"$tab"}
			case $line in
			"head$tab"*) head=$value ;;
			"failed$tab"*) failed=${failed:+$failed$nl}$value ;;
			"green$tab"*) green=$value ;;
			"seen$tab"*) seen=${seen:+$seen$nl}$value ;;
			"notes$tab"*) notes=$value ;;
			"review$tab"*) review=$value ;;
			"mergeable$tab"*) mergeable=$value ;;
			"broken$tab"*) broken=$value ;;
			esac
		done <"$state"
	fi
}

save_state() {
	curr=$1
	state=$state_dir/babysit-pr-$curr
	data="head$tab$head${nl}green$tab$green${nl}notes$tab$notes${nl}review$tab$review${nl}mergeable$tab$mergeable"
	data="$data${nl}broken$tab$broken$nl"
	while IFS= read -r name; do
		[ -n "$name" ] && data="${data}failed$tab$name$nl"
	done <<EOF
$failed
EOF
	while IFS= read -r url; do
		[ -n "$url" ] && data="${data}seen$tab$url$nl"
	done <<EOF
$seen
EOF
	if [ "$data" != "$saved" ]; then
		printf '%s' "$data" >"$state.new" && mv "$state.new" "$state" && saved=$data
	fi
}

look() {
	curr=$1
	load_state "$curr"
	# The comments, one line each in the order they showed: link, what it is, first line. A review's comments on the
	# diff show with it, at its time; a review still pending shows to its author only, and is not said.
	if ! view=$(gh pr view "$curr" --json state,mergeable,reviewDecision,headRefOid --jq '
		[.state, .mergeable, (.reviewDecision // ""), .headRefOid] | join("\u001f")' 2>"$errors") ||
		! now_notes=$(gh api graphql -F owner='{owner}' -F repo='{repo}' -F pr="$curr" -f query="$query" \
			--jq "$notes_jq" 2>"$errors"); then
		if [ "$broken" != yes ]; then
			IFS= read -r why <"$errors" || :
			printf '%s\n\n' "PR #$curr: gh failed, trying again: ${why:-}"
			broken=yes
			save_state "$curr"
		fi
		return 0
	fi
	broken=""
	case " $looked " in
	*" $curr "*) ;;
	*)
		looked="${looked:+$looked }$curr"
		failed="" green=""
		;;
	esac
	set -f
	old=$IFS
	IFS=$us
	# The fields, split on the unit separator only, without globbing.
	set -- $view
	IFS=$old
	set +f
	case ${1:-} in
	MERGED)
		printf '%s\n\n' "MERGED: PR #$curr is merged."
		return 1
		;;
	CLOSED)
		printf '%s\n\n' "CLOSED: PR #$curr is closed without a merge."
		return 1
		;;
	esac
	now_mergeable=${2:-} now_review=${3:-} now_head=${4:-}
	news="" more="" said=""
	if [ "$now_head" != "$head" ]; then
		# A new push: its checks start over.
		head=$now_head failed="" green=""
	fi

	# The checks: their number, whether they all pass (or skip), then those that fail, by name, each with its link.
	# gh pr checks exits non-zero while a check fails or is pending: its output is what counts, and none says nothing.
	checks=$(gh pr checks "$curr" --json name,bucket,link --jq '
		length, all(.bucket == "pass" or .bucket == "skipping"),
		(map(select(.bucket == "fail")) | unique_by(.name)[] | "\(.name)\t\(.link // "")")' 2>/dev/null)
	if [ -n "$checks" ]; then
		total=${checks%%"$nl"*}
		rest=${checks#*"$nl"}
		all=${rest%%"$nl"*}
		case $rest in
		*"$nl"*) now_failed=${rest#*"$nl"} ;;
		*) now_failed="" ;;
		esac
		short=$(printf '%.7s' "$head")
		new="" names=""
		while IFS= read -r line; do
			[ -n "$line" ] || continue
			name=${line%%"$tab"*}
			link=${line#*"$tab"}
			names=${names:+$names$nl}$name
			case "$nl$failed$nl" in
			*"$nl$name$nl"*) ;;
			*)
				new=${new:+$new, }$name
				# Its job's link: the lead reads its log there, and a re-run makes another.
				[ -n "$link" ] && more="$more${nl}$name: $link"
				;;
			esac
		done <<EOF
$now_failed
EOF
		if [ -n "$new" ]; then
			news="$news · checks failed at $short: $new"
			green=""
		fi
		# A check that runs again leaves the list: failing again, it is new.
		failed=$names
		if [ "$all" = true ] && [ "$total" -gt 0 ] && [ "$green" != yes ]; then
			news="$news · all checks pass at $short ($total)"
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
	# Each comment not read yet, known by its link, a paragraph of its own: its link on the first line, so that two
	# comments by one author never read as the same news. The first look says only the latest one.
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		url=${line%%"$us"*}
		rest=${line#*"$us"}
		case "$nl$seen$nl" in
		*"$nl$url$nl"*) continue ;;
		esac
		seen=${seen:+$seen$nl}$url
		paragraph="PR #$curr · ${rest%%"$us"*}: $url"
		excerpt=${rest#*"$us"}
		[ -n "$excerpt" ] && paragraph="$paragraph$nl> $excerpt"
		if [ "$notes" = yes ]; then
			said="$said$paragraph$nl$nl"
		else
			said="$paragraph$nl$nl"
		fi
	done <<EOF
$now_notes
EOF
	notes=yes

	if [ -n "$news" ]; then
		printf '%s\n\n' "PR #$curr$news$more"
	fi
	printf '%s' "$said"
	save_state "$curr"
	return 0
}

n=0
while :; do
	active_prs=""
	for p in $prs; do
		look "$p" && active_prs="${active_prs:+$active_prs }$p"
	done
	prs=$active_prs

	n=$((n + 1))
	if [ "$looks" -gt 0 ] && [ "$n" -ge "$looks" ]; then
		exit 0
	fi
	if [ -z "$prs" ]; then
		exit 0
	fi
	sleep "$every"
done
