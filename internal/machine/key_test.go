package machine

import (
	"strings"
	"testing"
)

// TestKey covers Key: the same command, run by another task in its own worktree and scratch folder, with other values
// in its environment, under sh -c or with its output filtered, has the same key; two commands that cost differently,
// a test of the whole module and one of a package, keep two.
func TestKey(t *testing.T) {
	const (
		worktree = "/home/dev/.config/djinn/projects/01a118fa-4308-736a-9ff9-5fc373a4294f/worktrees/01a1b6c3-8eec-7b18-86dd-bbf69031f639"
		other    = "/home/dev/.config/djinn/projects/01a118fa-4308-736a-9ff9-5fc373a4294f/worktrees/01a1b6c4-0000-7b18-86dd-bbf69031f639"
		scratch  = "/home/dev/.config/djinn/scratch/01a118fa-4308-736a-9ff9-5fc373a4294f/01a1b6c3-8eec-7b18-86dd-bbf69031f639"
	)
	for _, c := range []struct {
		name, command, dir, want string
	}{
		{"words one space apart", "  go   test\t ./...  ", "", "go test ./..."},
		{"scratch folder", "go build -o " + scratch + "/bin/djinn ./cmd/djinn", "", "go build -o $DJINN_SCRATCH/bin/djinn ./cmd/djinn"},
		{"scratch variable", "go build -o ${DJINN_SCRATCH}/bin/djinn ./cmd/djinn", "", "go build -o $DJINN_SCRATCH/bin/djinn ./cmd/djinn"},
		{"worktree of another task", "go test " + other + "/internal/gate", "", "go test $WORKTREE/internal/gate"},
		{"the folder it runs in", "go test " + worktree + "/internal/gate", worktree, "go test ./internal/gate"},
		{"a file under /tmp", "go test -coverprofile=/tmp/cover.3141.out ./internal/gate", "", "go test -coverprofile=/tmp/* ./internal/gate"},
		{"any other UUID", "./bin/probe --task 01a1b6c3-8eec-7b18-86dd-bbf69031f639", "", "./bin/probe --task $ID"},
		{"env values", "GOFLAGS=-race CGO_ENABLED=1 go test ./...", "", "GOFLAGS=* CGO_ENABLED=* go test ./..."},
		{"env command", "env -i HOME=/x go test ./...", "", "HOME=* go test ./..."},
		{"export and cd before it", "export PATH=/usr/local/go/bin:$PATH && cd " + worktree + " && go tool task lint", "", "go tool task lint"},
		{"sh -c", "sh -c 'go tool task ui && npx vite build --config e2e/vite.config.ts'", "", "go tool task ui && npx vite build --config e2e/vite.config.ts"},
		{"bash -lc in a wrapper", `timeout 600 bash -lc "cd x; go test ./..."`, "", "go test ./..."},
		{"nice and nohup", "nice -n 10 nohup go test ./...", "", "go test ./..."},
		{"taskset", "taskset -c 0-15 npx playwright test e2e/docs.spec.ts", "", "npx playwright test e2e/docs.spec.ts"},
		{"filters and redirections", "go test ./... 2>&1 | tail -50", "", "go test ./..."},
		{"output to a file", "go test ./... > " + scratch + "/out.log 2>&1; echo EXIT=$?", "", "go test ./..."},
		{"quotes kept where needed", `go test -run 'TestA|TestB' ./internal/gate`, "", `go test -run 'TestA|TestB' ./internal/gate`},
		{"a here-document's body", "python3 - <<'EOF'\nprint(1)\nEOF", "", "python3 -"},
		{"only a set up", "cd " + worktree, "", "cd $WORKTREE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Key(c.command, c.dir)
			if got != c.want {
				t.Errorf("Key(%q) = %q, want %q", c.command, got, c.want)
			}
			if again := Key(got); again != got {
				t.Errorf("Key(Key(%q)) = %q, not %q", c.command, again, got)
			}
		})
	}

	// A test of the whole module and one of a package cost apart: two keys (W10, global and local).
	if global, local := Key("go test ./..."), Key("go test ./internal/gate"); global == local {
		t.Errorf("a global and a local test share the key %q", global)
	}
	// Two tasks running the same command in their own worktree and scratch folder find one key.
	a := Key("cd "+worktree+" && go build -o "+scratch+"/djinn ./cmd/djinn 2>&1 | tail -5", worktree)
	b := Key("go build -o $DJINN_SCRATCH/djinn ./cmd/djinn", other)
	if a != b {
		t.Errorf("one command, two keys: %q and %q", a, b)
	}
	// A long command is cut at a word, within KeyLength.
	long := Key(strings.Repeat("word ", 100))
	if len(long) > KeyLength || strings.HasSuffix(long, " ") || !strings.HasSuffix(long, "word") {
		t.Errorf("a long key: %q", long)
	}
	if got := Key(strings.Repeat("—", 150)); len(got) > KeyLength || strings.ContainsRune(got, '�') {
		t.Errorf("a long word cut in a rune: %q", got)
	}
}

// TestHead covers Head: the program and its subcommand, the arguments left out.
func TestHead(t *testing.T) {
	for key, want := range map[string]string{
		"go test ./internal/gate -run TestKey":     "go test",
		"go test -race ./...":                      "go test",
		"go tool task lint":                        "go tool task lint",
		"npm run build":                            "npm run build",
		"npx playwright test e2e/wish.spec.ts":     "npx playwright test",
		"GOFLAGS=* go build ./cmd/djinn":           "go build",
		"./bin/djinn-e2e up --data $DJINN_SCRATCH": "./bin/djinn-e2e up",
		"python3 -I bench.py":                      "python3",
		"":                                         "",
	} {
		if got := Head(key); got != want {
			t.Errorf("Head(%q) = %q, want %q", key, got, want)
		}
	}
}
