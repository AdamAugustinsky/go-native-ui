//go:build windows && arm64

#include "textflag.h"
#include "go_asm.h"
#include "funcdata.h"

// Outgoing stack arguments occupy 0..127; the Frame pointer is saved at 136.
#define STACK_SIZE 144
#define PTR_ADDRESS (STACK_SIZE - 8)

#define COPYSTACK(i) \
	MOVD (Frame_stack+(i*8))(R9), R10 \
	MOVD R10, (i*8)(RSP)

GLOBL ·thunkAddr(SB), NOPTR|RODATA, $8
DATA ·thunkAddr(SB)/8, $gonativeui_com_thunk(SB)

// Called by syscall.SyscallN (on the thread's system stack, with the Windows
// ARM64 C ABI): R0 = *Frame. Loads x0-x7, d0-d7 and the stack arguments from
// the Frame, calls Frame.fn and stores x0 in Frame.r0. The technique is the
// one of internal/ffi (ffi_arm64.s); Windows reserves x18, which is not used
// here, and uses AAPCS64's register assignment for these argument kinds.
TEXT gonativeui_com_thunk(SB), NOSPLIT, $0
	SUB  $STACK_SIZE, RSP
	MOVD R0, PTR_ADDRESS(RSP)
	MOVD R0, R9

	FMOVD (Frame_floats+0)(R9), F0
	FMOVD (Frame_floats+8)(R9), F1
	FMOVD (Frame_floats+16)(R9), F2
	FMOVD (Frame_floats+24)(R9), F3
	FMOVD (Frame_floats+32)(R9), F4
	FMOVD (Frame_floats+40)(R9), F5
	FMOVD (Frame_floats+48)(R9), F6
	FMOVD (Frame_floats+56)(R9), F7

	COPYSTACK(0)
	COPYSTACK(1)
	COPYSTACK(2)
	COPYSTACK(3)
	COPYSTACK(4)
	COPYSTACK(5)
	COPYSTACK(6)
	COPYSTACK(7)
	COPYSTACK(8)
	COPYSTACK(9)
	COPYSTACK(10)
	COPYSTACK(11)
	COPYSTACK(12)
	COPYSTACK(13)
	COPYSTACK(14)
	COPYSTACK(15)

	MOVD (Frame_ints+0)(R9), R0
	MOVD (Frame_ints+8)(R9), R1
	MOVD (Frame_ints+16)(R9), R2
	MOVD (Frame_ints+24)(R9), R3
	MOVD (Frame_ints+32)(R9), R4
	MOVD (Frame_ints+40)(R9), R5
	MOVD (Frame_ints+48)(R9), R6
	MOVD (Frame_ints+56)(R9), R7

	MOVD Frame_fn(R9), R10
	BL   (R10)

	MOVD PTR_ADDRESS(RSP), R2
	ADD  $STACK_SIZE, RSP

	MOVD  R0, Frame_r0(R2)
	FMOVD F0, Frame_f0(R2)
	RET
