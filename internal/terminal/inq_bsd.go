//go:build !windows && !linux

package terminal

// inputQueued is the ioctl that tells how many bytes of input wait to be read on a terminal: FIONREAD, _IOR('f', 127,
// int) on macOS and the BSDs, which x/sys does not name there.
const inputQueued = 0x4004667f
