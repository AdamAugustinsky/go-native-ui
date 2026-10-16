//go:build darwin && arm64

#include "textflag.h"
#include "go_asm.h"
#include "funcdata.h"

#define STACK_SIZE 80
#define PTR_ADDRESS (STACK_SIZE - 8)

GLOBL ·trampABI0(SB), NOPTR|RODATA, $8
DATA ·trampABI0(SB)/8, $gnui_probe_ffi_call(SB)

// Called on g0 via runtime.cgocall with C ABI: R0 = *Frame.
TEXT gnui_probe_ffi_call(SB), NOSPLIT, $0
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

	MOVD (Frame_stack+0)(R9), R10
	MOVD R10, 0(RSP)
	MOVD (Frame_stack+8)(R9), R10
	MOVD R10, 8(RSP)
	MOVD (Frame_stack+16)(R9), R10
	MOVD R10, 16(RSP)
	MOVD (Frame_stack+24)(R9), R10
	MOVD R10, 24(RSP)
	MOVD (Frame_stack+32)(R9), R10
	MOVD R10, 32(RSP)
	MOVD (Frame_stack+40)(R9), R10
	MOVD R10, 40(RSP)
	MOVD (Frame_stack+48)(R9), R10
	MOVD R10, 48(RSP)
	MOVD (Frame_stack+56)(R9), R10
	MOVD R10, 56(RSP)

	MOVD (Frame_ints+0)(R9), R0
	MOVD (Frame_ints+8)(R9), R1
	MOVD (Frame_ints+16)(R9), R2
	MOVD (Frame_ints+24)(R9), R3
	MOVD (Frame_ints+32)(R9), R4
	MOVD (Frame_ints+40)(R9), R5
	MOVD (Frame_ints+48)(R9), R6
	MOVD (Frame_ints+56)(R9), R7
	MOVD Frame_r8(R9), R8

	MOVD Frame_fn(R9), R10
	BL   (R10)

	MOVD PTR_ADDRESS(RSP), R2
	ADD  $STACK_SIZE, RSP

	MOVD  R0, Frame_r0(R2)
	MOVD  R1, Frame_r1(R2)
	FMOVD F0, (Frame_f+0)(R2)
	FMOVD F1, (Frame_f+8)(R2)
	FMOVD F2, (Frame_f+16)(R2)
	FMOVD F3, (Frame_f+24)(R2)
	RET

GLOBL ·cbTableABI0(SB), NOPTR|RODATA, $8
DATA ·cbTableABI0(SB)/8, $gnui_probe_cbtable(SB)

TEXT gnui_probe_cbtable(SB), NOSPLIT|NOFRAME, $0
	MOVD $0, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $1, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $2, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $3, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $4, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $5, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $6, R12
	B    gnui_probe_cbcommon(SB)
	MOVD $7, R12
	B    gnui_probe_cbcommon(SB)

// Frame layout (192 bytes, 16-aligned):
//   0..16    saved R27, R30
//  16..64    cbArgs
//  64..192   saved F0-F7, R0-R7 (contiguous with caller stack args at 192)
TEXT gnui_probe_cbcommon(SB), NOSPLIT|NOFRAME, $0
	NO_LOCAL_POINTERS
	SUB   $(24*8), RSP
	ADD   $(8*8), RSP, R14
	FSTPD (F0, F1), (0*8)(R14)
	FSTPD (F2, F3), (2*8)(R14)
	FSTPD (F4, F5), (4*8)(R14)
	FSTPD (F6, F7), (6*8)(R14)
	STP   (R0, R1), (8*8)(R14)
	STP   (R2, R3), (10*8)(R14)
	STP   (R4, R5), (12*8)(R14)
	STP   (R6, R7), (14*8)(R14)
	STP   (R27, R30), 0(RSP)

	ADD  $16, RSP, R13
	MOVD R12, cbArgs_index(R13)
	MOVD R14, cbArgs_args(R13)
	MOVD R8, cbArgs_result(R13)

	MOVD ·cbDispatchCall(SB), R0
	MOVD (R0), R0
	MOVD R13, R1
	MOVD $0, R3
	BL   crosscall2(SB)

	ADD   $16, RSP, R13
	MOVD  (cbArgs_result+0)(R13), R0
	MOVD  (cbArgs_result+8)(R13), R1
	FMOVD (cbArgs_result+0)(R13), F0
	FMOVD (cbArgs_result+8)(R13), F1
	FMOVD (cbArgs_result+16)(R13), F2
	FMOVD (cbArgs_result+24)(R13), F3

	LDP 0(RSP), (R27, R30)
	ADD $(24*8), RSP
	RET
