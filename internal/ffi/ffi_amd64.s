//go:build (darwin || linux) && amd64

#include "textflag.h"
#include "go_asm.h"
#include "funcdata.h"

// Outgoing stack arguments occupy 0..96; the frame pointer is saved at 104.
#define STACK_SIZE 112
#define PTR_ADDRESS (STACK_SIZE - 8)

#define COPYSTACK(i) \
	MOVQ (Frame_stack+(i*8))(R11), R12 \
	MOVQ R12, (i*8)(SP)

GLOBL ·callABI0(SB), NOPTR|RODATA, $8
DATA ·callABI0(SB)/8, $gonativeui_ffi_call(SB)

// Called on g0 by runtime.cgocall with the C ABI: DI = *Frame.
TEXT gonativeui_ffi_call(SB), NOSPLIT, $STACK_SIZE
	MOVQ DI, PTR_ADDRESS(SP)
	MOVQ DI, R11

	MOVQ (Frame_floats+0)(R11), X0
	MOVQ (Frame_floats+8)(R11), X1
	MOVQ (Frame_floats+16)(R11), X2
	MOVQ (Frame_floats+24)(R11), X3
	MOVQ (Frame_floats+32)(R11), X4
	MOVQ (Frame_floats+40)(R11), X5
	MOVQ (Frame_floats+48)(R11), X6
	MOVQ (Frame_floats+56)(R11), X7

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

	MOVQ (Frame_ints+0)(R11), DI
	MOVQ (Frame_ints+8)(R11), SI
	MOVQ (Frame_ints+16)(R11), DX
	MOVQ (Frame_ints+24)(R11), CX
	MOVQ (Frame_ints+32)(R11), R8
	MOVQ (Frame_ints+40)(R11), R9
	MOVQ Frame_nfloat(R11), AX // AL = vector registers used (variadic callees)

	MOVQ Frame_fn(R11), R10
	CALL R10

	MOVQ PTR_ADDRESS(SP), DI
	MOVQ AX, Frame_r0(DI)
	MOVQ DX, Frame_r1(DI)
	MOVQ X0, (Frame_f+0)(DI)
	MOVQ X1, (Frame_f+8)(DI)
	RET

GLOBL ·cbTableABI0(SB), NOPTR|RODATA, $8
DATA ·cbTableABI0(SB)/8, $gonativeui_ffi_cbtable(SB)

// Each entry is a 5-byte CALL; the return address identifies the slot.
TEXT gonativeui_ffi_cbtable(SB), NOSPLIT|NOFRAME, $0
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)
	CALL gonativeui_ffi_cbcommon(SB)

// On entry: 0(SP) = return address into the table, 8(SP) = return address
// to the C caller, 16(SP)... = the caller's stack arguments.
TEXT gonativeui_ffi_cbcommon(SB), NOSPLIT|NOFRAME, $0
	MOVQ 0(SP), AX  // table return address -> slot index
	MOVQ 8(SP), R10 // C return address (overwritten below, restored before RET)
	ADDQ $8, SP     // drop the table return address

	// Save X0-X7 and DI,SI,DX,CX,R8,R9 at 8..120(SP). R9 lands where the C
	// return address was, so the argument block runs straight into the
	// caller's stack arguments.
	ADJSP $14*8, SP
	MOVSD X0, (1*8)(SP)
	MOVSD X1, (2*8)(SP)
	MOVSD X2, (3*8)(SP)
	MOVSD X3, (4*8)(SP)
	MOVSD X4, (5*8)(SP)
	MOVSD X5, (6*8)(SP)
	MOVSD X6, (7*8)(SP)
	MOVSD X7, (8*8)(SP)
	MOVQ  DI, (9*8)(SP)
	MOVQ  SI, (10*8)(SP)
	MOVQ  DX, (11*8)(SP)
	MOVQ  CX, (12*8)(SP)
	MOVQ  R8, (13*8)(SP)
	MOVQ  R9, (14*8)(SP)
	LEAQ  8(SP), R8 // argument block

	PUSHQ R10

	// index = (retaddr - table) / 5 - 1
	MOVQ $gonativeui_ffi_cbtable(SB), DX
	SUBQ DX, AX
	MOVQ $0, DX
	MOVQ $5, CX
	DIVL CX
	SUBQ $1, AX

	SUBQ $(24+cbArgs__size), SP
	MOVQ AX, (24+cbArgs_index)(SP)
	MOVQ R8, (24+cbArgs_args)(SP)
	MOVQ DI, (24+cbArgs_result)(SP) // hidden struct-return pointer, if any
	MOVQ $0, (24+cbArgs_result+8)(SP)
	MOVQ $0, (24+cbArgs_result+16)(SP)
	MOVQ $0, (24+cbArgs_result+24)(SP)
	LEAQ 24(SP), AX

	// crosscall2(fn, frame, 0, ctxt) -> runtime.cgocallback -> cbDispatch(frame)
	MOVQ ·cbDispatchCall(SB), DI
	MOVQ (DI), DI
	MOVQ AX, SI
	MOVQ $0, CX
	CALL crosscall2(SB)

	MOVQ (24+cbArgs_result)(SP), AX
	MOVQ (24+cbArgs_result+8)(SP), DX
	MOVQ (24+cbArgs_result+16)(SP), X0
	MOVQ (24+cbArgs_result+24)(SP), X1
	ADDQ $(24+cbArgs__size), SP

	POPQ  R10
	ADJSP $-14*8, SP
	MOVQ  R10, 0(SP)
	RET
