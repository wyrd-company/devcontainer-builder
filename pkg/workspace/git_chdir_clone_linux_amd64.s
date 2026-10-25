// relationships:
//   references: [git_chdir_clone_linux]

#include "go_asm.h"
#include "textflag.h"

// func gitCloneThread(stack uintptr, state *gitCloneState) int64
//
// The new thread starts on stack with the registers of this one. It keeps
// state in R12, which system calls preserve, and never returns to Go.
TEXT ·gitCloneThread(SB),NOSPLIT,$0-24
	MOVQ	state+8(FP), R12
	MOVQ	$const_gitCloneFlags, DI
	MOVQ	stack+0(FP), SI
	LEAQ	gitCloneState_tid(R12), DX	// parent thread ID
	MOVQ	DX, R10	// child thread ID
	XORQ	R8, R8	// no TLS: CLONE_SETTLS is clear
	MOVQ	$const_gitCloneSysClone, AX
	SYSCALL
	CMPQ	AX, $0
	JEQ	thread
	MOVQ	AX, ret+16(FP)
	RET

thread:
	MOVQ	$const_gitCloneSysGetpid, AX
	SYSCALL
	MOVQ	AX, gitCloneState_pid(R12)
	XORQ	DI, DI	// SIG_BLOCK with no set only reads the mask
	XORQ	SI, SI
	LEAQ	gitCloneState_mask(R12), DX
	MOVQ	$const_gitCloneSigsetSize, R10
	MOVQ	$const_gitCloneSysSigprocmask, AX
	SYSCALL
	MOVQ	gitCloneState_fd(R12), DI
	MOVQ	$const_gitCloneSysFchdir, AX
	SYSCALL
	CMPQ	AX, $0
	JNE	finished
	LEAQ	gitCloneState_path(R12), DI
	MOVQ	$const_gitClonePathSize, SI
	MOVQ	$const_gitCloneSysGetcwd, AX
	SYSCALL

finished:
	MOVQ	AX, gitCloneState_result(R12)

exit:
	XORQ	DI, DI
	MOVQ	$const_gitCloneSysExit, AX	// ends this thread only
	SYSCALL
	JMP	exit
