#!/bin/sh
# The inbox source of the babysit-pr skill. Djinn runs it only once you plug it in on your machine
# (djinn inbox plug babysit-pr, or "Plug in" in the inbox), every five minutes, in the project's folder. It prints one
# paragraph per open pull request of this repository assigned to you or that requests your review: a line, then its
# link. An assigned one starts with "Babysit PR #<n>", so the inbox proposes the skill's wish. It only reads, with gh
# logged in by itself: it comments, reviews and marks nothing. Then it exits.
#
# Usage: inbox.sh. Needs gh, logged in.
set -u
status=0
gh pr list --search "assignee:@me" --json number,title,url \
	--jq '.[] | "Babysit PR #\(.number): \(.title) (assigned to you)\n\(.url)\n"' || status=1
gh pr list --search "review-requested:@me" --json number,title,url \
	--jq '.[] | "Review PR #\(.number): \(.title) (your review is requested)\n\(.url)\n"' || status=1
exit $status
