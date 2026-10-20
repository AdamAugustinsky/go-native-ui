#!/bin/sh
# Exit 0 if the console session is unlocked, 3 if it is locked (or cannot be
# read). Live captures need an unlocked screen; run this first, and do not
# wait or poll: report what is left.
#
#	sh tools/appkitref/unlocked.sh && caffeinate -d sh live.sh OUT
exec python3 - <<'PY'
import ctypes, ctypes.util, sys
cg = ctypes.CDLL(ctypes.util.find_library("CoreGraphics"))
cf = ctypes.CDLL(ctypes.util.find_library("CoreFoundation"))
cg.CGSessionCopyCurrentDictionary.restype = ctypes.c_void_p
d = cg.CGSessionCopyCurrentDictionary()
if not d:
    print("no console session", file=sys.stderr)
    sys.exit(3)
cf.CFDictionaryGetValue.restype = ctypes.c_void_p
cf.CFDictionaryGetValue.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
cf.CFStringCreateWithCString.restype = ctypes.c_void_p
cf.CFStringCreateWithCString.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_uint32]
yes = ctypes.c_void_p.in_dll(cf, "kCFBooleanTrue").value
v = cf.CFDictionaryGetValue(d, cf.CFStringCreateWithCString(None, b"CGSSessionScreenIsLocked", 0x08000100))
if v == yes:
    print("the screen is locked", file=sys.stderr)
    sys.exit(3)
PY
