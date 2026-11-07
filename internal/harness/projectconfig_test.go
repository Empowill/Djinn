package harness

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
)

// TestProjectContainers: the ceilings come from the project's djinn.yaml, the root's when both files are there, with a
// warning naming the one not read; an unreadable file holds Djinn's defaults, warned once with the file and the field,
// and is still named as the file where the project sets them. Serial: it captures Djinn's log, which is global.
func TestProjectContainers(t *testing.T) {
	var log bytes.Buffer
	defer logx.Use(logx.New(&log, slog.LevelDebug))()
	cases := []struct {
		name  string
		files map[string]string
		file  string
		pids  uint32
		warn  []string
	}{
		{name: "none"},
		{name: "agents folder", files: map[string]string{".agents/djinn.yaml": "containers: {pids: 99}\n"},
			file: ".agents/djinn.yaml", pids: 99},
		{name: "both", files: map[string]string{"djinn.yaml": "containers: {pids: 64}\n", ".agents/djinn.yaml": "containers: {pids: 99}\n"},
			file: "djinn.yaml", pids: 64, warn: []string{"two files configure Djinn", "ignored=.agents/djinn.yaml"}},
		{name: "unknown field", files: map[string]string{"djinn.yaml": "containers: {pid: 64}\n"},
			file: "djinn.yaml", warn: []string{"defaults hold", "djinn.yaml: unknown field containers.pid"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log.Reset()
			dir := t.TempDir()
			for name, content := range c.files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h := &Harness{home: t.TempDir()}
			project := &planv1.Project{Id: "p", Directory: dir}
			for range 2 {
				got, file := h.projectContainers(project)
				if file != c.file || got.GetPids() != c.pids {
					t.Errorf("containers %v from %q, want pids %d from %q", got, file, c.pids, c.file)
				}
			}
			for _, w := range c.warn {
				if strings.Count(log.String(), w) != 1 {
					t.Errorf("log %q, want %q once", log.String(), w)
				}
			}
			if c.warn == nil && log.Len() > 0 {
				t.Errorf("log %q, want none", log.String())
			}
		})
	}
}
