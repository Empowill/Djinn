package terminal

import "golang.org/x/sys/unix"

// inputQueued is the ioctl that tells how many bytes of input wait to be read on a terminal: FIONREAD.
const inputQueued = unix.TIOCINQ
