package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// machineServer serves the gates and the commands' costs of a store of its own, as djinn up does, holding one project
// named lamp; it returns the server's address and the project.
func machineServer(t *testing.T) (addr string, project *planv1.Project) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project = &planv1.Project{Id: store.NewID(), Name: "lamp", Directory: folder, CreateTime: timestamppb.Now()}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.ProjectServiceAddProcedure,
			&planv1.ProjectServiceAddRequest{Directory: project.GetDirectory()}), tx.Put(project))
	}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, route := range []func() (string, http.Handler){
		func() (string, http.Handler) { return gate.Handler(gate.New(nil, nil, nil)) },
		func() (string, http.Handler) { return machine.CostsHandler(db) },
	} {
		path, h := route()
		mux.Handle(path, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, project
}

// TestRunGate covers runGate, djinn gate run: its arguments, the command it runs under the gate the server grants,
// whose exit code it returns, and the djinn it cannot reach. The command is this test binary, running no test, in a
// folder outside any git worktree: the tree gate.Run records with its cost would read the whole repository.
func TestRunGate(t *testing.T) {
	addr, _ := machineServer(t)
	t.Chdir(t.TempDir())
	for _, c := range []struct {
		name string
		env  map[string]string
		args []string
		code int
		out  string // in the command's output
		errs string // in djinn's
	}{
		{name: "help", args: []string{"--help"}, out: "Usage: djinn gate run"},
		{name: "a second name", args: []string{"test", "lint", "--", "true"}, code: 2, errs: `unexpected argument "lint"`},
		{name: "no command", args: []string{"test", "--addr", addr}, code: 2, errs: "a gate name and a command after -- are required"},
		{name: "runs the command", args: []string{"test", "--addr", addr, "--task-id", "", "--", os.Args[0], "-test.run=^$"}, out: "PASS"},
		{name: "its exit code", args: []string{"--addr=" + addr, "--task-id=", "test", "--", os.Args[0], "-test.bogus"}, code: 2,
			errs: "flag provided but not defined"},
		{name: "no djinn runs", env: map[string]string{"DJINN_HOME": t.TempDir(), "DJINN_ADDR": ""},
			args: []string{"test", "--", "true"}, code: 1, errs: "no djinn answers: run djinn up first"},
		{name: "djinn is gone", args: []string{"test", "--addr", "http://127.0.0.1:1", "--", "true"}, code: 1, errs: "error: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			var out, errs bytes.Buffer
			code := runGate(c.args, strings.NewReader(""), &out, &errs)
			if code != c.code || !strings.Contains(out.String(), c.out) || !strings.Contains(errs.String(), c.errs) {
				t.Errorf("djinn gate run %q = %d\n%s%s", c.args, code, out.String(), errs.String())
			}
		})
	}
}
