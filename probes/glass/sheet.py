#!/usr/bin/env python3
# sheet.py OUT.png ROWS FILE... : stack the top ROWS pixel rows of each PNG
# capture (8-bit RGB/RGBA, non-interlaced) with a magenta divider. Pure
# Python (no PIL on the test Mac).
import struct, sys, zlib

def readpng(p):
    d = open(p, 'rb').read()
    i, idat = 8, b''
    while i < len(d):
        n = struct.unpack('>I', d[i:i + 4])[0]
        t, c = d[i + 4:i + 8], d[i + 8:i + 8 + n]
        i += 12 + n
        if t == b'IHDR':
            w, h, bd, ct = struct.unpack('>IIBB', c[:10])
        elif t == b'IDAT':
            idat += c
    raw = zlib.decompress(idat)
    bpp = {2: 3, 6: 4}[ct]
    stride = w * bpp
    rows, prev, j = [], bytearray(stride), 0
    for y in range(h):
        f = raw[j]; j += 1
        line = bytearray(raw[j:j + stride]); j += stride
        for x in range(stride):
            a = line[x - bpp] if x >= bpp else 0
            b = prev[x]
            c = prev[x - bpp] if x >= bpp else 0
            if f == 1: line[x] = (line[x] + a) & 255
            elif f == 2: line[x] = (line[x] + b) & 255
            elif f == 3: line[x] = (line[x] + ((a + b) >> 1)) & 255
            elif f == 4:
                pa, pb, pc = abs(b - c), abs(a - c), abs(a + b - 2 * c)
                line[x] = (line[x] + (a if pa <= pb and pa <= pc else b if pb <= pc else c)) & 255
        rows.append(bytes(line) if bpp == 3 else bytes(v for k, v in enumerate(line) if k % 4 != 3))
        prev = line
    return w, rows

def writepng(p, w, rows):
    raw = b''.join(b'\x00' + r for r in rows)
    ch = lambda t, c: struct.pack('>I', len(c)) + t + c + struct.pack('>I', zlib.crc32(t + c) & 0xffffffff)
    open(p, 'wb').write(b'\x89PNG\r\n\x1a\n' + ch(b'IHDR', struct.pack('>IIBBBBB', w, len(rows), 8, 2, 0, 0, 0)) + ch(b'IDAT', zlib.compress(raw, 6)) + ch(b'IEND', b''))

out, n = sys.argv[1], int(sys.argv[2])
allrows, W = [], None
for f in sys.argv[3:]:
    w, rows = readpng(f)
    W = W or w
    allrows += [r[:W * 3].ljust(W * 3, b'\x00') for r in rows[:n]] + [b'\xff\x00\xff' * W] * 6
writepng(out, W, allrows)
