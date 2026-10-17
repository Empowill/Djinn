package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotating_Write: the file rolls over before a line takes it past its size, keeps that many older files and drops
// the oldest, so all of them stay under (keep+1) × size.
func TestRotating_Write(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	r, err := OpenRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two lines a file.
	for i := range 9 {
		if _, err := fmt.Fprintf(r, "%d%s", i, line[1:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{File: "8", File + ".1": "67", File + ".2": "45"}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(want) {
		t.Fatalf("files %v, want %d", entries, len(want))
	}
	total := 0
	for name, lines := range want {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
		var firsts string
		for l := range strings.Lines(string(b)) {
			firsts += l[:1]
		}
		if firsts != lines {
			t.Errorf("%s holds lines %q, want %q", name, firsts, lines)
		}
	}
	if total > 300 {
		t.Errorf("the files hold %d bytes, over (2+1) × 100", total)
	}
}

// TestOpenRotating: a djinn up that starts again appends to its log, and counts what it held.
func TestOpenRotating(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), File)
	for range 2 {
		r, err := OpenRotating(path, 50, 1)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(r, strings.Repeat("a", 30))
		r.Close()
	}
	if b, _ := os.ReadFile(path); len(b) != 30 {
		t.Errorf("%s holds %d bytes, want the second start's 30", path, len(b))
	}
	if b, _ := os.ReadFile(path + ".1"); len(b) != 30 {
		t.Errorf("%s.1 holds %d bytes, want the first start's 30", path, len(b))
	}
}
