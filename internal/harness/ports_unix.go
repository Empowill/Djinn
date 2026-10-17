//go:build !windows

package harness

// servePIDs lists the processes of the serve p: its process group, what carries its mark, and its scope's cgroup.
func servePIDs(p *process) []int {
	ids := leftBy(p, true)
	pids := make([]int, 0, len(ids)+1)
	for _, id := range ids {
		pids = append(pids, id.pid)
	}
	if len(pids) == 0 && p.cmd.Process != nil {
		pids = append(pids, p.cmd.Process.Pid)
	}
	return pids
}
