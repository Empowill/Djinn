package server

import (
	"net/http"
	"net/http/pprof"
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
