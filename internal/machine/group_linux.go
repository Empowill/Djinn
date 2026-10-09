package machine

import "os"

// NotMeasured is why the workers' CPU and memory are not measured here; empty: they are.
const NotMeasured = ""

// listProcs lists the processes from /proc.
func listProcs() ([]proc, error) { return readProcs(os.DirFS("/")) }
