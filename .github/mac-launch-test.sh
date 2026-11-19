#!/bin/sh
# The launch test on a Mac (W156): proves that a djinn built from a branch admits and runs work on a real Mac, where the
# kernel's memory pressure decides the workers and the gates (W152). Run by hand through
# .github/workflows/mac-launch.yml; it runs on Linux too, the macOS readings skipped.
#
# Usage: mac-launch-test.sh <djinn binary> <work folder>
#
# It starts a djinn of its own on a data folder inside the work folder (never a real one), with a fake claude first on
# its PATH so no model is called, then spawns a task on the fake provider whose script runs one command under a gate
# (djinn gate run), and runs one more gated command from outside the worker. It prints what macOS reports, what Djinn
# admits and why, the task's start and end, and the gates' admissions. It fails when the work does not start within
# START_LIMIT seconds or does not end within END_LIMIT. Off macOS, the macOS readings are skipped.
set -eu

djinn_bin=$1
work=$2
START_LIMIT=${START_LIMIT:-240}
END_LIMIT=${END_LIMIT:-240}

case $djinn_bin in /*) ;; *) djinn_bin=$(pwd)/$djinn_bin ;; esac
mkdir -p "$work"
work=$(cd "$work" && pwd)
home=$work/home
rm -rf "$home"
mkdir -p "$home/bin" "$home/shell" "$home/user"

say() { printf '\n== %s\n' "$*"; }

macos_readings() {
	say "macOS reports ($1)"
	if [ "$(uname -s)" != Darwin ]; then
		echo "not macOS ($(uname -s)): readings skipped"
		return 0
	fi
	sysctl kern.memorystatus_vm_pressure_level vm.swapusage hw.memsize hw.ncpu || true
	# memory_pressure prints the page counts, then the free percentage.
	memory_pressure | tail -n 4 || true
}

# A fake claude, as e2e/agents.ts writes it: the lead's batch gets one canned turn, anything else says how it was
# called and keeps its input open. No model runs here.
cat >"$home/bin/claude" <<'EOF'
#!/bin/sh
headless=0
for a in "$@"; do [ "$a" = -p ] && headless=1; done
if [ "$headless" = 1 ] && [ "${DJINN_LEAD:-}" = 1 ]; then
  read -r _ || true
  echo '{"type":"assistant","session_id":"019d0000-0000-7000-8000-000000000099","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"Handled the batch."}]}}'
  echo '{"type":"result","subtype":"success","session_id":"019d0000-0000-7000-8000-000000000099","total_cost_usd":0.01}'
  exit 0
fi
echo "fake-claude $* in $(pwd)"
exec cat
EOF
chmod +x "$home/bin/claude"
printf '[core]\n\tautocrlf = false\n' >"$home/gitconfig"

export DJINN_HOME="$home"
export HOME="$home/user"
export GIT_CONFIG_GLOBAL="$home/gitconfig"
export PATH="$home/bin:$PATH"
export SHELL=/bin/sh
unset DJINN_ADDR DJINN_TASK_ID DJINN_LEAD 2>/dev/null || true
d() { "$djinn_bin" "$@"; }

macos_readings "before djinn up"

say "djinn up, data folder $home"
d version
d up --browser --terminal-dir "$home/shell" >"$work/up.out" 2>"$work/up.err" &
up_pid=$!
stop() {
	kill -INT "$up_pid" 2>/dev/null || true
	i=0
	while kill -0 "$up_pid" 2>/dev/null && [ $i -lt 20 ]; do sleep 1; i=$((i + 1)); done
	kill -KILL "$up_pid" 2>/dev/null || true
}
trap stop EXIT
i=0
until grep -q 'http://127.0.0.1:[0-9]*/?token=' "$work/up.out" 2>/dev/null; do
	if ! kill -0 "$up_pid" 2>/dev/null || [ $i -ge 30 ]; then
		echo "djinn up did not start:"
		cat "$work/up.out" "$work/up.err"
		exit 1
	fi
	sleep 1
	i=$((i + 1))
done
until d machine show >/dev/null 2>&1; do sleep 1; done
echo "djinn up answers (pid $up_pid)"

say "What Djinn admits and why (djinn machine show)"
d machine show
d load get || true

say "Spawn a task on the fake provider: one command under a gate, then a while of work"
wish=$(d wish make "Mac launch test" --json | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' | head -n 1)
echo "wish $wish"
d wish activate "$wish" >/dev/null 2>&1 || true
prompt='text The Mac launch test starts.
djinn gate run launch -- /bin/sh -c "echo gated in the worker"
sleep 10s
text The Mac launch test ends.'
task=$(d task spawn "$wish" --title "Mac launch test" --provider fake --prompt "$prompt" --json |
	sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' | head -n 1)
echo "task $task"

state() { d task get "$task" --json | sed -n 's/.*"status": *"\([A-Z_]*\)".*/\1/p' | head -n 1; }
waited=0
last=""
while :; do
	s=$(state)
	[ "$s" != "$last" ] && echo "after ${waited}s: $s" && last=$s
	case $s in *RUNNING | *DONE | *FAILED | *STOPPED | *CANCELLED) break ;; esac
	if [ $waited -ge "$START_LIMIT" ]; then
		echo "FAIL: the task did not start within ${START_LIMIT}s"
		d task get "$task" || true
		d machine show || true
		macos_readings "when the task did not start"
		grep -iE 'slot|pressure|admit|waits|start' "$home/logs/djinn.log" | tail -n 40 || true
		exit 1
	fi
	sleep 2
	waited=$((waited + 2))
done
echo "STARTED after ${waited}s"
macos_readings "while the task runs"
say "djinn machine show while the task runs"
d machine show

ended=0
while :; do
	s=$(state)
	[ "$s" != "$last" ] && echo "after ${ended}s more: $s" && last=$s
	case $s in *DONE) break ;; *FAILED | *STOPPED | *CANCELLED)
		echo "FAIL: the task ended $s"
		d task get "$task" || true
		exit 1
		;;
	esac
	if [ $ended -ge "$END_LIMIT" ]; then
		echo "FAIL: the task did not end within ${END_LIMIT}s of its start"
		d task get "$task" || true
		exit 1
	fi
	sleep 2
	ended=$((ended + 2))
done
echo "ENDED: $s"

say "The task's events (the gated command's output among them)"
d task watch "$task" --last 50 >"$work/events.out" 2>&1 || true
tail -n 30 "$work/events.out"
if ! grep -q 'gated in the worker' "$work/events.out"; then
	echo "FAIL: the gated command did not run in the worker"
	exit 1
fi

say "One more command under a gate, from outside a worker"
d gate run launch -- /bin/sh -c 'echo gated outside a worker'

say "The gates' admissions (djinn gate history)"
d gate history || true

macos_readings "at the end"

say "Djinn's admission log (djinn.log)"
grep -iE 'slot|pressure|admit|gate|worker|start' "$home/logs/djinn.log" | tail -n 60 || true

say "PASS: Djinn admitted and ran the work, and the gate admitted its commands"
