#!/usr/bin/env python3
"""Pilot's checkpoint sheets and measured table: Pilot beside AppKit's finder scene.

    python3 checkpoint.py --out ~/Projects/research/appkit-ref/d6/pilot [--live DIR] [--set legacy|overlay|both]

It renders the scene offscreen with `go run ./cmd/pilot/pilotshot` at the finder scene's size (1120x640 pt, 2x), reads
its frames (-measure) and AppKit's (tools/appkitscenes JSON, the reference set cmd/chromedemo -measure's
appkitref.Select picks: captures-macmini for legacy scroll bars, captures-macmini-overlay for overlay ones), and writes
  measured.md   element, ours, AppKit, delta: geometry in points (tolerance 0.5) and fills in ΔE2000 (tolerance 2)
  sheets/       AppKit (left) and ours (right): offscreen, and live when --live has GnuiShot captures
The known exceptions are marked: the accent (ours follows this Mac's pink, the references are blue) and the system glass
(it adapts to what is behind it; offscreen the sidebar is its fallback colour).
"""
import argparse, json, math, os, subprocess, sys
from PIL import Image, ImageDraw, ImageFont

REF = os.path.expanduser('~/Projects/research/appkit-ref/scenes')
SETS = {'legacy': 'captures-macmini', 'overlay': 'captures-macmini-overlay'}
REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', '..'))

# ---- colour: sRGB -> CIELAB, ΔE2000 ----
def lab(rgb):
    def lin(c):
        c /= 255
        return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4
    r, g, b = (lin(c) for c in rgb[:3])
    x = (0.4124564 * r + 0.3575761 * g + 0.1804375 * b) / 0.95047
    y = 0.2126729 * r + 0.7151522 * g + 0.0721750 * b
    z = (0.0193339 * r + 0.1191920 * g + 0.9503041 * b) / 1.08883
    f = lambda t: t ** (1 / 3) if t > 216 / 24389 else (24389 / 27 * t + 16) / 116
    fx, fy, fz = f(x), f(y), f(z)
    return 116 * fy - 16, 500 * (fx - fy), 200 * (fy - fz)

def de2000(c1, c2):
    L1, a1, b1 = lab(c1); L2, a2, b2 = lab(c2)
    C1, C2 = math.hypot(a1, b1), math.hypot(a2, b2)
    Cb = (C1 + C2) / 2
    G = 0.5 * (1 - math.sqrt(Cb ** 7 / (Cb ** 7 + 25 ** 7)))
    a1p, a2p = (1 + G) * a1, (1 + G) * a2
    C1p, C2p = math.hypot(a1p, b1), math.hypot(a2p, b2)
    h = lambda b, a: math.degrees(math.atan2(b, a)) % 360
    h1p, h2p = h(b1, a1p), h(b2, a2p)
    dL, dC = L2 - L1, C2p - C1p
    if C1p * C2p == 0: dh = 0
    else:
        dh = h2p - h1p
        if dh > 180: dh -= 360
        elif dh < -180: dh += 360
    dH = 2 * math.sqrt(C1p * C2p) * math.sin(math.radians(dh / 2))
    Lb, Cbp = (L1 + L2) / 2, (C1p + C2p) / 2
    if C1p * C2p == 0: hb = h1p + h2p
    elif abs(h1p - h2p) <= 180: hb = (h1p + h2p) / 2
    elif h1p + h2p < 360: hb = (h1p + h2p + 360) / 2
    else: hb = (h1p + h2p - 360) / 2
    T = 1 - 0.17 * math.cos(math.radians(hb - 30)) + 0.24 * math.cos(math.radians(2 * hb)) + 0.32 * math.cos(math.radians(3 * hb + 6)) - 0.20 * math.cos(math.radians(4 * hb - 63))
    dth = 30 * math.exp(-(((hb - 275) / 25) ** 2))
    Rc = 2 * math.sqrt(Cbp ** 7 / (Cbp ** 7 + 25 ** 7))
    Sl = 1 + 0.015 * (Lb - 50) ** 2 / math.sqrt(20 + (Lb - 50) ** 2)
    Sc, Sh = 1 + 0.045 * Cbp, 1 + 0.015 * Cbp * T
    Rt = -math.sin(math.radians(2 * dth)) * Rc
    return math.sqrt((dL / Sl) ** 2 + (dC / Sc) ** 2 + (dH / Sh) ** 2 + Rt * (dC / Sc) * (dH / Sh))

# ---- ours ----
def run(args, **kw):
    return subprocess.run(['go', 'run', './cmd/pilot/pilotshot'] + args, cwd=REPO, check=True, capture_output=True, text=True, **kw)

def ours(out, mode, inactive, scrolled, legacy):
    flags = ['-mode', mode, '-w', '2240', '-h', '1280']
    if inactive: flags.append('-inactive')
    if scrolled: flags.append('-scrolled')
    if legacy: flags.append('-legacy')
    png = os.path.join(out, 'offscreen', f"pilot-{'legacy' if legacy else 'overlay'}-{mode}-{'inactive' if inactive else 'active'}-{'scrolled' if scrolled else 'top'}.png")
    os.makedirs(os.path.dirname(png), exist_ok=True)
    run(['-o', png] + flags)
    nodes = json.loads(run(['-measure'] + flags).stdout)
    return png, nodes

def node(nodes, role, name, nth=0):
    m = [n for n in nodes if n['Role'] == role and n['Name'] == name]
    return m[nth]['Frame'] if len(m) > nth else None

# ---- AppKit ----
def appkit(setname, mode, state, pos):
    base = os.path.join(REF, SETS[setname], f'finder-{mode}-{state}-{pos}')
    return base + '.png', json.load(open(base + '.json'))

def hexrgb(s):
    s = s.split('#')[-1]
    return tuple(int(s[i:i + 2], 16) for i in (0, 2, 4))

def px(img, x, y):
    return img.getpixel((int(x * 2 + 1e-6), int(y * 2 + 1e-6)))[:3]

def ink_left(img, x0, y0, x1, y1, thr=140):
    """Leftmost column (points) with a pixel darker than thr in the box, -1 if none."""
    for X in range(int(x0 * 2), int(x1 * 2)):
        for Y in range(int(y0 * 2), int(y1 * 2)):
            p = img.getpixel((X, Y))[:3]
            if (p[0] + p[1] + p[2]) / 3 < thr: return X / 2
    return -1

def rows_for(setname, mode, inactive, scrolled):
    legacy = setname == 'legacy'
    state = 'inactive' if inactive else 'active'
    pos = 'scrolled' if scrolled else 'top'
    opng, nodes = ours(OUT, mode, inactive, scrolled, legacy)
    apng, aj = appkit(setname, mode, state, pos)
    oi, ai = Image.open(opng).convert('RGBA'), Image.open(apng).convert('RGBA')
    R = []  # (element, ours, appkit, delta, unit, note)
    def geo(name, o, a, tol=0.5, note=''):
        if o is None or a is None: R.append((name, o, a, None, 'pt', note or 'not comparable')); return
        d = o - a
        R.append((name, round(o, 2), round(a, 2), round(d, 2), 'pt', note + ('' if abs(d) <= tol else ' MISS')))
    def col(name, o, a, note='', tol=2.0):
        d = de2000(o, a)
        R.append((name, '#%02X%02X%02X' % o, '#%02X%02X%02X' % a, round(d, 2), 'ΔE', note + ('' if d <= tol or note.startswith('exception') else ' MISS')))
    tb_a = [i for i in aj['toolbarItems'] if i['id'] == 'NSToolbarToggleSidebarItem'][0]['viewFrame']
    tbl_a = aj['tables'][0]
    side_a = aj['tables'][1]
    tb_o = node(nodes, 'Group', 'Toolbar')
    geo('toolbar height', tb_o[3], aj['window']['titlebarPlusToolbarHeight'])
    so = node(nodes, 'Button', 'Sidebar')
    for k, n in enumerate('xywh'):
        geo(f'sidebar button {n}', so[k], [tb_a['x'], tb_a['y'], tb_a['w'], tb_a['h']][k])
    nav = node(nodes, 'Group', 'Back/Forward')
    nav_a = None
    def walk(v):
        nonlocal nav_a
        if isinstance(v, dict):
            if v.get('class') == 'NSToolbarPlatterView' and v['frame']['w'] == 86 and v['frame']['x'] < 300: nav_a = v['frame']
            for x in v.values(): walk(x)
        elif isinstance(v, list):
            for x in v: walk(x)
    walk(aj['tree'])
    for k, n in enumerate('xywh'):
        geo(f'back/forward capsule {n}', nav[k], [nav_a['x'], nav_a['y'], nav_a['w'], nav_a['h']][k])
    up = node(nodes, 'Button', 'Enclosing Folder'); title = node(nodes, 'Label', next(n['Name'] for n in nodes if n['Role'] == 'Label' and n['Frame'][1] > 10 and n['Frame'][1] < 20 and n['Frame'][0] > 200))
    tf = None
    def walk2(v):
        nonlocal tf
        if isinstance(v, dict):
            if v.get('class') == '_NSToolbarTitleField': tf = v['frame']
            for x in v.values(): walk2(x)
        elif isinstance(v, list):
            for x in v: walk2(x)
    walk2(aj['tree'])
    geo('title: gap after the last capsule', title[0] - (up[0] + up[2]), tf['x'] - (nav_a['x'] + nav_a['w']))
    geo('title y', title[1], tf['y']); geo('title h', title[3], tf['h'])
    sb = node(nodes, 'List', 'Sidebar')
    geo('sidebar width', sb[2], aj['splitItems'][0]['viewFrame']['w'])
    hdr_a = side_a['rows'][0]['rect']; hdr_o = node(nodes, 'Group', 'Favorites')
    geo('sidebar group header y', hdr_o[1], hdr_a['y'], note='' if not scrolled else "AppKit's capture scrolls its sidebar too") if not scrolled else None
    geo('sidebar group header h', hdr_o[3], hdr_a['h'])
    row1_a = side_a['rows'][1]['rect']
    first = next(n for n in nodes if n['Role'] == 'ListItem' and n['Frame'][0] < 5)
    if not scrolled: geo('sidebar first row y', first['Frame'][1], row1_a['y'])
    geo('sidebar row h', first['Frame'][3], row1_a['h'])
    nxt = [n for n in nodes if n['Role'] == 'ListItem' and n['Frame'][0] < 5][1]
    geo('sidebar row pitch', nxt['Frame'][1] - first['Frame'][1], side_a['rows'][2]['rect']['y'] - row1_a['y'])
    t_o = node(nodes, 'Table', 'files')
    geo('table x', t_o[0], tbl_a['frame']['x']); geo('table header y', t_o[1], tbl_a['header']['y'])
    nh = node(nodes, 'ColumnHeader', 'Name')
    geo('table header h', nh[3], tbl_a['header']['h'])
    rows_o = [n for n in nodes if n['Role'] == 'Row' and n['Frame'][0] < 400]
    r0 = tbl_a['rows'][0]['rect']
    if not scrolled:
        geo('first row y', rows_o[0]['Frame'][1], r0['y'])
    geo('row h', rows_o[0]['Frame'][3], r0['h'])
    geo('row pitch', rows_o[1]['Frame'][1] - rows_o[0]['Frame'][1], tbl_a['rows'][1]['rect']['y'] - r0['y'])
    img_o = next(n for n in nodes if n['Role'] == 'Image' and n['Frame'][2] == 16)
    ia = tbl_a['rows'][0]['image']['frame']
    if not scrolled:
        geo('file icon x from the table', img_o['Frame'][0] - t_o[0], ia['x'] - tbl_a['frame']['x'])
        geo('file icon y from its row', img_o['Frame'][1] - rows_o[0]['Frame'][1], ia['y'] - r0['y'])
    geo('file icon w', img_o['Frame'][2], ia['w']); geo('file icon h', img_o['Frame'][3], ia['h'])
    # header title ink
    ox = ink_left(oi, t_o[0], 56, t_o[0] + 100, 76); ax = ink_left(ai, tbl_a['frame']['x'], 56, tbl_a['frame']['x'] + 100, 76)
    geo('header "Name" ink x from the table', ox - t_o[0], ax - tbl_a['frame']['x'], note='ink, 0.5 pt steps')
    # fills
    ty_o = rows_o[0]['Frame'][1]
    def rowy(rows, i, y0, pitch=24): return y0 + i * pitch
    # alternating rows: rows 0 and 1 (not scrolled), else the stripe parity from the row's index
    def probe_row(img, x, y): return px(img, x, y + 1.5)
    if not scrolled:
        col('row 0 fill (white)', probe_row(oi, t_o[0] + 60 + 240, rows_o[0]['Frame'][1]), probe_row(ai, tbl_a['frame']['x'] + 300, r0['y']))
        col('row 1 fill (alternating)', probe_row(oi, t_o[0] + 300, rows_o[1]['Frame'][1]), probe_row(ai, tbl_a['frame']['x'] + 300, tbl_a['rows'][1]['rect']['y']))
    col('toolbar pocket (behind the header)', px(oi, t_o[0] + 100, 66), px(ai, tbl_a['frame']['x'] + 100, 66), note='' if not scrolled else '')
    # (scrolled: AppKit's selected rows are under the toolbar's glass and the header's line shows what is under it)
    if not scrolled:
        sel_a = next(r for r in tbl_a['rows'] if r.get('selected'))
        sel_o = next(n for n in rows_o if n['Name'] == 'ui')  # the scene selects latest, ui and .gitignore
        name = 'selected row fill (emphasised: key window, list focused)' if not inactive else 'selected row fill (unemphasised)'
        col(name, probe_row(oi, t_o[0] + 300, sel_o['Frame'][1]), probe_row(ai, tbl_a['frame']['x'] + 300, sel_a['rect']['y']))
        # the line under the header
        col('header line (under the header)', px(oi, t_o[0] + 100, 79.75), px(ai, tbl_a['frame']['x'] + 100, 79.75))
    # sidebar selected row
    selrow_a = next((r for r in side_a['rows'] if r.get('selected')), None)
    selrow_o = next((n for n in nodes if n['Role'] == 'ListItem' and n['Name'] == 'Documents'), None)
    if selrow_a and selrow_o:
        col('sidebar selection fill', px(oi, 140, selrow_o['Frame'][1] + 16), px(ai, 140, selrow_a['rect']['y'] + 16))
    col('sidebar fill (glass)', px(oi, 4, 300), px(ai, 4, 300), note='exception: the system glass adapts to what is behind it; offscreen ours is its fallback colour')
    return opng, apng, R, nodes

def md_table(title, R):
    s = f'### {title}\n\n| element | ours | AppKit | delta | |\n|---|---|---|---|---|\n'
    for name, o, a, d, unit, note in R:
        s += f'| {name} | {o} | {a} | {"" if d is None else str(d) + " " + unit} | {note} |\n'
    return s + '\n'

def label(im, text, h=34):
    out = Image.new('RGB', (im.width, im.height + h), 'white')
    out.paste(im.convert('RGB'), (0, h))
    d = ImageDraw.Draw(out)
    try: f = ImageFont.truetype('/System/Library/Fonts/SFNS.ttf', 22)
    except Exception: f = ImageFont.load_default()
    d.text((12, 5), text, fill='black', font=f)
    return out

def sheet(a_png, o_png, title, path):
    a, o = label(Image.open(a_png), 'AppKit (macOS 27.2): ' + title), label(Image.open(o_png), 'Pilot: ' + title)
    W = Image.new('RGB', (a.width + o.width + 20, max(a.height, o.height)), (200, 200, 200))
    W.paste(a, (0, 0)); W.paste(o, (a.width + 20, 0))
    os.makedirs(os.path.dirname(path), exist_ok=True)
    W.save(path)

if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('--out', default=os.path.expanduser('~/Projects/research/appkit-ref/d6/pilot'))
    ap.add_argument('--live', default='')
    ap.add_argument('--set', default='both')
    A = ap.parse_args()
    OUT = A.out
    md = '# Pilot against AppKit\'s finder scene\n\nElement, ours, AppKit, delta. Points for geometry (±0.5), ΔE2000 for fills (≤ 2).\n\n'
    for sname in (['legacy', 'overlay'] if A.set == 'both' else [A.set]):
        for mode in ('light', 'dark'):
            for inactive in (False, True):
                for scrolled in (False, True):
                    opng, apng, R, _ = rows_for(sname, mode, inactive, scrolled)
                    tag = f"{sname}-{mode}-{'inactive' if inactive else 'active'}-{'scrolled' if scrolled else 'top'}"
                    md += md_table(tag, R)
                    sheet(apng, opng, tag + ' (offscreen)', os.path.join(OUT, 'sheets', 'offscreen-' + tag + '.png'))
                    if A.live:
                        lp = os.path.join(A.live, f'pilot-{tag}.png')
                        if os.path.exists(lp): sheet(apng, lp, tag + ' (live)', os.path.join(OUT, 'sheets', 'live-' + tag + '.png'))
    open(os.path.join(OUT, 'measured.md'), 'w').write(md)
    print('wrote', os.path.join(OUT, 'measured.md'))
