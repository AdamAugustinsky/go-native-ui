//go:build (darwin || linux) && arm64

#include "textflag.h"
#include "go_asm.h"
#include "funcdata.h"

// Outgoing stack arguments occupy 0..96; the frame pointer is saved at 104.
#define STACK_SIZE 112
#define PTR_ADDRESS (STACK_SIZE - 8)

#define COPYSTACK(i) \
	MOVD (Frame_stack+(i*8))(R9), R10 \
	MOVD R10, (i*8)(RSP)

GLOBL ·callABI0(SB), NOPTR|RODATA, $8
DATA ·callABI0(SB)/8, $gonativeui_ffi_call(SB)

// Called on g0 by runtime.cgocall with the C ABI: R0 = *Frame.
TEXT gonativeui_ffi_call(SB), NOSPLIT, $0
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

// One table entry: 8 bytes (a single-instruction MOVD and a B), so entry i
// is at cbtable + 8*i. The macros expand to MaxCallbacks entries.
#define CBENTRY(i) MOVD $(i), R12; B gonativeui_ffi_cbcommon(SB)
#define CB4(i) CBENTRY(i); CBENTRY((i)+1); CBENTRY((i)+2); CBENTRY((i)+3)
#define CB16(i) CB4(i); CB4((i)+4); CB4((i)+8); CB4((i)+12)
#define CB64(i) CB16(i); CB16((i)+16); CB16((i)+32); CB16((i)+48)
#define CB256(i) CB64(i); CB64((i)+64); CB64((i)+128); CB64((i)+192)

GLOBL ·cbTableABI0(SB), NOPTR|RODATA, $8
DATA ·cbTableABI0(SB)/8, $gonativeui_ffi_cbtable(SB)

TEXT gonativeui_ffi_cbtable(SB), NOSPLIT|NOFRAME, $0
	CB256(0)
	CB256(256)
	CB256(512)
	CB256(768)

// Callback frame (CBSTACK bytes; cbArgs and Frame sizes are multiples of 16):
//   0..16              saved R27, R30
//   CBARGS..           cbArgs
//   CBFRAME..          the invocation's own Frame (cbArgs.frame)
//   CBREGS..CBSTACK    saved F0-F7, R0-R7; contiguous with the caller's stack args
#define CBARGS 16
#define CBFRAME (CBARGS+cbArgs__size)
#define CBREGS (CBFRAME+Frame__size)
#define CBSTACK (CBREGS+16*8)

TEXT gonativeui_ffi_cbcommon(SB), NOSPLIT|NOFRAME, $0
	NO_LOCAL_POINTERS
	SUB   $CBSTACK, RSP
	ADD   $CBREGS, RSP, R14
	FSTPD (F0, F1), (0*8)(R14)
	FSTPD (F2, F3), (2*8)(R14)
	FSTPD (F4, F5), (4*8)(R14)
	FSTPD (F6, F7), (6*8)(R14)
	STP   (R0, R1), (8*8)(R14)
	STP   (R2, R3), (10*8)(R14)
	STP   (R4, R5), (12*8)(R14)
	STP   (R6, R7), (14*8)(R14)
	STP   (R27, R30), 0(RSP)

	ADD  $CBARGS, RSP, R13
	ADD  $CBFRAME, RSP, R15
	MOVD R12, cbArgs_index(R13)
	MOVD R14, cbArgs_args(R13)
	MOVD R15, cbArgs_frame(R13)
	MOVD R8, cbArgs_result(R13)
	MOVD ZR, (cbArgs_result+8)(R13)
	MOVD ZR, (cbArgs_result+16)(R13)
	MOVD ZR, (cbArgs_result+24)(R13)

	// crosscall2(fn, frame, 0, ctxt) -> runtime.cgocallback -> cbDispatch(frame)
	MOVD ·cbDispatchCall(SB), R0
	MOVD (R0), R0
	MOVD R13, R1
	MOVD $0, R3
	BL   crosscall2(SB)

	ADD   $CBARGS, RSP, R13
	MOVD  (cbArgs_result+0)(R13), R0
	MOVD  (cbArgs_result+8)(R13), R1
	FMOVD (cbArgs_result+0)(R13), F0
	FMOVD (cbArgs_result+8)(R13), F1
	FMOVD (cbArgs_result+16)(R13), F2
	FMOVD (cbArgs_result+24)(R13), F3

	LDP 0(RSP), (R27, R30)
	ADD $CBSTACK, RSP
	RET
