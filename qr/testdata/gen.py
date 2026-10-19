#!/usr/bin/env python3
"""Generates vectors.txt: reference QR symbols from an independent encoder.

The Go tests (qr/vectors_test.go) re-encode every line with our encoder and
compare the version, the mask the encoder chooses and a hash of all modules.
The reference is segno (https://pypi.org/project/segno/, BSD-3), used here only
as an oracle, never imported by the repository:

    pip3 install --target /tmp/pylib segno
    PYTHONPATH=/tmp/pylib python3 gen.py > vectors.txt

Line format:  level kind arg n version mask sha256
  kind byte|num|alnum: arg is the seed of the generator below, n the length;
  kind text: arg is the payload in hex (n is its length).
sha256 is of the modules, row by row, packed into bytes most significant bit
first (rows are not padded; the last byte is padded with zeros).
"""
import hashlib
import segno
from segno import encoder


# segno pads an already byte-aligned bit stream with a whole extra zero byte
# (`8 - length % 8`, never 0) where ISO/IEC 18004 7.4.10 adds nothing. That
# only moves the pad codewords of a symbol whose data ends on a byte boundary
# after the terminator, so the reference is corrected here.
def _padding_bits(buff, version, length):
    buff.extend([0] * ((8 - length % 8) % 8))


encoder.write_padding_bits = _padding_bits

# Mask selection (ISO/IEC 18004 7.8.3) leaves two points open and no reader
# depends on them, since every mask decodes: whether the format and version
# information are drawn when a mask is scored, and whether a 1:1:3:1:1 pattern
# with light areas on both sides counts once or twice. We score the finished
# symbol and count once per occurrence, as ZXing, Nayuki's QR Code generator
# and python-qrcode do (see the package comment of qr). segno reads the standard
# otherwise, so the reference below is made to follow our reading; what the
# vectors then check is the encoding and the arithmetic of rules N1 to N4, not
# which reading is normative.
#
# segno scores the masks before the format and version information are drawn
# (the reserved areas count as light, and the dark module as light too). We,
# like ZXing, Nayuki's generator and python-qrcode, score the finished symbol,
# so the reference is made to do the same. The scoring rules themselves (N1 to
# N4) stay segno's own.
_orig_encode = encoder.encode
_ctx = {}


def _encode(content, error=None, *args, **kwargs):
    _ctx["error"] = encoder.normalize_errorlevel(error, accept_none=True)
    return _orig_encode(content, error, *args, **kwargs)


def _best_mask(matrix, width, height, proposed_mask=None):
    version = (width - 17) // 4
    funcs = encoder.get_data_mask_functions(False)
    fm = encoder.make_matrix(width, height)
    encoder.add_finder_patterns(fm, width, height)
    encoder.add_alignment_patterns(fm, width, height)
    fm[-8][8] = 0x1
    region = lambda i, j: fm[i][j] > 0x1
    if proposed_mask is not None:
        encoder.apply_mask(matrix, funcs[proposed_mask], width, height, region)
        return proposed_mask, matrix
    best, best_score, best_m = None, None, None
    for n, f in enumerate(funcs):
        m = tuple(bytearray(r) for r in matrix)
        encoder.apply_mask(m, f, width, height, region)
        probe = tuple(bytearray(r) for r in m)
        encoder.add_format_info(probe, version, _ctx["error"], n)
        encoder.add_version_info(probe, version)
        score = encoder.evaluate_mask(probe, width, height)
        if best_score is None or score < best_score:
            best, best_score, best_m = n, score, m
    return best, best_m




# segno's N3 search skips the 1011101 cores that overlap one it just counted;
# the standard counts every occurrence (a core with four light modules before
# or after it, the symbol edge counting as light). N3 is replaced by that
# plain reading, in a different style from the Go code (strings, not a window).
def _n3(matrix, width):
    total = 0
    for rows in (matrix, list(zip(*matrix))):
        for r in rows:
            line = "0000" + "".join("1" if b else "0" for b in r) + "0000"
            for i in range(4, len(line) - 10):  # i: the core's first module
                if line[i:i + 7] == "1011101" and (line[i - 4:i] == "0000" or line[i + 7:i + 11] == "0000"):
                    total += 40
    return total


_orig_scores = encoder.mask_scores


def _scores(matrix, width, height):
    n1, n2, _, n4 = _orig_scores(matrix, width, height)
    return n1, n2, _n3(matrix, width), n4


encoder.mask_scores = _scores
encoder.encode = _encode
encoder.find_and_apply_best_mask = _best_mask

ALNUM = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"
CAP = {  # bytes at L M Q H, digits at L, alnum at L (ISO/IEC 18004 Table 7)
}


def fill(seed, n):
    x, out = seed, bytearray()
    for _ in range(n):
        x = (x * 1103515245 + 12345) & 0x7FFFFFFF
        out.append((x >> 16) & 0xFF)
    return bytes(out)


def payload(kind, arg, n):
    if kind == "byte":
        return fill(arg, n), "byte"
    if kind == "num":
        return bytes(48 + b % 10 for b in fill(arg, n)), "numeric"
    if kind == "alnum":
        return bytes(ord(ALNUM[b % 45]) for b in fill(arg, n)), "alphanumeric"
    return bytes.fromhex(arg), None


def digest(q):
    bits = "".join(str(b) for row in q.matrix for b in row)
    bits += "0" * (-len(bits) % 8)
    return hashlib.sha256(int(bits, 2).to_bytes(len(bits) // 8, "big")).hexdigest()


def emit(level, kind, arg, n):
    data, mode = payload(kind, arg, n)
    if mode is None:
        # Mirror our rule: numeric if all digits, alphanumeric if all in the
        # table, else byte (the empty payload is numeric).
        s = data.decode("latin-1")
        mode = "numeric" if all(c.isdigit() and c < "\x80" for c in s) else \
            "alphanumeric" if all(c in ALNUM for c in s) else "byte"
    if mode == "byte":
        q = segno.make(data, error=level, mode="byte", micro=False, boost_error=False)
    else:
        q = segno.make(data.decode("ascii"), error=level, mode=mode, micro=False, boost_error=False)
    a = arg if kind == "text" else str(arg)
    print(level, kind, a or "-", n, q.version, q.mask, digest(q))


# Table 7 byte capacities, to fill every version/level to the last byte.
BYTES = """17 14 11 7|32 26 20 14|53 42 32 24|78 62 46 34|106 84 60 44|134 106 74 58|154 122 86 64|192 152 108 84|230 180 130 98|271 213 151 119|321 251 177 137|367 287 203 155|425 331 241 177|458 362 258 194|520 412 292 220|586 450 322 250|644 504 364 280|718 560 394 310|792 624 442 338|858 666 482 382|929 711 509 403|1003 779 565 439|1091 857 611 461|1171 911 661 511|1273 997 715 535|1367 1059 751 593|1465 1125 805 625|1528 1190 868 658|1628 1264 908 698|1732 1370 982 742|1840 1452 1030 790|1952 1538 1112 842|2068 1628 1168 898|2188 1722 1228 958|2303 1809 1283 983|2431 1911 1351 1051|2563 1989 1423 1093|2699 2099 1499 1139|2809 2213 1579 1219|2953 2331 1663 1273""".split("|")
caps = [[int(x) for x in r.split()] for r in BYTES]

for v, row in enumerate(caps, 1):
    for li, level in enumerate("LMQH"):
        emit(level, "byte", v * 7 + li, row[li])
        if v in (1, 3, 9, 10, 18, 27, 33, 40):  # just past the previous version
            if v > 1:
                emit(level, "byte", v * 11 + li, caps[v - 2][li] + 1)

for v, (nd, na) in {1: (41, 25), 2: (77, 47), 7: (370, 224), 10: (652, 395), 26: (3283, 1990), 40: (7089, 4296)}.items():
    for level in "LMQH":
        emit(level, "num", v, nd // (1 + "LMQH".index(level)))
        emit(level, "alnum", v, na // (1 + "LMQH".index(level)))
    emit("L", "num", v + 100, nd - 1)
    emit("L", "alnum", v + 100, na - 1)

for s in ["A", "0", "HELLO WORLD", "01234567", "https://example.com", "hello, world", "AC-42",
          "WIFI:T:WPA;S:Cafe;P:coffee-2026;;", "http://a3lab.ai/", "日本語のテキスト", "3.14159265358979",
          "https://wa.me/qr/" + "ABCDEFGHIJKLM" * 3 + "?src=zapfast&ref=pairing&session=0123456789abcdef0123456789abcdef"]:
    for level in "LMQH":
        emit(level, "text", s.encode().hex(), len(s.encode()))
