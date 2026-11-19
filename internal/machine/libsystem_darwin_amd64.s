// The trampolines of libsystem_darwin.go, as golang.org/x/sys/unix writes its own (zsyscall_darwin_*.s).

#include "textflag.h"

TEXT libc_proc_pid_rusage_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pid_rusage(SB)
GLOBL	·procPidRusageAddr(SB), RODATA, $8
DATA	·procPidRusageAddr(SB)/8, $libc_proc_pid_rusage_trampoline<>(SB)

TEXT libc_host_statistics64_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_host_statistics64(SB)
GLOBL	·hostStatistics64Addr(SB), RODATA, $8
DATA	·hostStatistics64Addr(SB)/8, $libc_host_statistics64_trampoline<>(SB)

TEXT libc_mach_host_self_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_host_self(SB)
GLOBL	·machHostSelfAddr(SB), RODATA, $8
DATA	·machHostSelfAddr(SB)/8, $libc_mach_host_self_trampoline<>(SB)
