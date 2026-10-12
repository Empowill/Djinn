package server

import (
	"fmt"
	"net/http"
	"net/http/pprof"

	"github.com/empowill/djinn/internal/logx"
)

// ProfilePath is where Profiles serves.
const ProfilePath = "/debug/pprof/"

// Profiles serves Go's profiles of this process (net/http/pprof) under ProfilePath, for djinn up --pprof: mounted
// among the services, it is reached only as they are, through the Unix socket or the loopback server and its token.
// The CPU profile of 30 s: curl --unix-socket <data folder>/djinn.sock -o cpu.prof
// http://djinn/debug/pprof/profile?seconds=30, then go tool pprof cpu.prof.
func Profiles() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ProfilePath, pprof.Index) // heap, goroutine, allocs, block, mutex… by name
	mux.HandleFunc(ProfilePath+"cmdline", pprof.Cmdline)
	mux.HandleFunc(ProfilePath+"profile", pprof.Profile)
	mux.HandleFunc(ProfilePath+"symbol", pprof.Symbol)
	mux.HandleFunc(ProfilePath+"trace", pprof.Trace)
	return mux
}

// DumpPath is where Dump answers.
const DumpPath = "/debug/dump"

// Dump answers a POST by writing the stacks of every goroutine to a new file of dir (logx.Dump) and its path as the
// body, for djinn debug dump: always mounted, it is reached only as the services are. A djinn whose calls hang still
// answers it, on a goroutine of its own.
func Dump(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST to dump the goroutines", http.StatusMethodNotAllowed)
			return
		}
		path, err := logx.Dump(dir, "asked by djinn debug dump")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, path)
	})
}
