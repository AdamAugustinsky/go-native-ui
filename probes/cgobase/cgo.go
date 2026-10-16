//go:build darwin && cgo

package cgobase

/*
#include <stdlib.h>
extern int goCmp(void*, void*);
static int cmpTramp(const void* a, const void* b) { return goCmp((void*)a, (void*)b); }
static long arr[2];
static void sort2(void) { arr[0]=2; arr[1]=1; qsort(arr, 2, sizeof(long), cmpTramp); }
*/
import "C"
import "unsafe"

var sink int

func cLabs(i int) int { return int(C.labs(C.long(i))) }
func cSort2()         { C.sort2() }

//export goCmp
func goCmp(a, b unsafe.Pointer) C.int {
	x, y := *(*C.long)(a), *(*C.long)(b)
	if x < y {
		return -1
	}
	if x > y {
		return 1
	}
	return 0
}
