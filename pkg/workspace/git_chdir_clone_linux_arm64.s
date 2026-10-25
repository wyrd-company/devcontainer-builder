// relationships:
//   references: [git_chdir_clone_linux]

#include "go_asm.h"
#include "textflag.h"

// func gitCloneThread(stack uintptr, state *gitCloneState) int64
//
// The new thread starts on stack with the registers of this one. It keeps
// state in R20, which system calls preserve, and never returns to Go.
// arm64's clone takes flags, stack, parent thread ID, TLS, child thread ID.
TEXT ·gitCloneThread(SB),NOSPLIT|NOFRAME,$0-24
	MOVD	state+8(FP), R20
	MOVD	$const_gitCloneFlags, R0
	MOVD	stack+0(FP), R1
	ADD	$gitCloneState_tid, R20, R2	// parent thread ID
	MOVD	ZR, R3	// no TLS: CLONE_SETTLS is clear
	MOVD	R2, R4	// child thread ID
	MOVD	$const_gitCloneSysClone, R8
	SVC
	CMP	ZR, R0
	BEQ	thread
	MOVD	R0, ret+16(FP)
	RET

thread:
	MOVD	$const_gitCloneSysGetpid, R8
	SVC
	MOVD	R0, gitCloneState_pid(R20)
	MOVD	ZR, R0	// SIG_BLOCK with no set only reads the mask
	MOVD	ZR, R1
	ADD	$gitCloneState_mask, R20, R2
	MOVD	$const_gitCloneSigsetSize, R3
	MOVD	$const_gitCloneSysSigprocmask, R8
	SVC
	MOVD	gitCloneState_fd(R20), R0
	MOVD	$const_gitCloneSysFchdir, R8
	SVC
	CBNZ	R0, finished
	ADD	$gitCloneState_path, R20, R0
	MOVD	$const_gitClonePathSize, R1
	MOVD	$const_gitCloneSysGetcwd, R8
	SVC

finished:
	MOVD	R0, gitCloneState_result(R20)

exit:
	MOVD	ZR, R0
	MOVD	$const_gitCloneSysExit, R8	// ends this thread only
	SVC
	B	exit
