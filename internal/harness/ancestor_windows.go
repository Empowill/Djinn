//go:build windows

package harness

func isDescendant(peerPID, workerPID int, _ string) bool {
	return peerPID > 0 && peerPID == workerPID
}
