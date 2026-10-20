#!/usr/bin/env python3
# summarize.py DIR: the cost matrix from costs.sh's JSON (medians over the
# rounds where the window stayed active).
import glob, json, statistics as st, sys
d0 = sys.argv[1]
cfgs = ['base', 'tbpocket', 'items', 'vev', 'glass', 'drawn', 'drawnk', 'fgasync', 'fgtx', 'fgonecb']
print('%-9s %-7s %-12s %-14s %-15s %-13s %-15s %-14s %-13s %s' % ('config', 'rounds', 'drops/run', 'after target', 'present lat', 'our GPU ms', 'blur GPU ms', 'main ms', 'WS CPU %', 'other'))
for c in cfgs:
    rows = [json.load(open(f)) for f in sorted(glob.glob(f'{d0}/{c}-r*-active-anim.json'))]
    ok = [d for d in rows if d['active']]
    if not ok:
        continue
    m = lambda fn: st.median([fn(d) for d in ok])
    other = ''
    if 'fgPresentedApart' in ok[0]:
        other = 'fg/content on different vsyncs: %s of %s' % (sum(d['fgPresentedApart']['apart'] for d in ok), sum(d['fgPresentedApart']['both'] for d in ok))
    blur = '%.3f/%.3f' % (m(lambda d: d['gpuBlurMs']['p50']), m(lambda d: d['gpuBlurMs']['p95'])) if 'gpuBlurMs' in ok[0] else '-'
    pat = '%.1f/%.1f' % (m(lambda d: d['presentedAfterTargetMs']['p50']), m(lambda d: d['presentedAfterTargetMs']['p95'])) if 'presentedAfterTargetMs' in ok[0] else '-'
    print('%-9s %-7s %-12s %-14s %-15s %-13s %-15s %-14s %-13s %s' % (c, '%d/%d' % (len(ok), len(rows)), ','.join(str(d['intervalsOver1.5Frames']) for d in ok), pat,
          '%.1f/%.1f' % (m(lambda d: d['latencyMs']['p50']), m(lambda d: d['latencyMs']['p95'])),
          '%.3f/%.3f' % (m(lambda d: d['gpuMs']['p50']), m(lambda d: d['gpuMs']['p95'])), blur,
          '%.2f/%.2f' % (m(lambda d: d['mainThreadMs']['p50']), m(lambda d: d['mainThreadMs']['p95'])),
          '%.1f [%s]' % (m(lambda d: d['load']['windowServerCPUPct']), ','.join('%.0f' % d['load']['windowServerCPUPct'] for d in ok)), other))
print('\nidle (6 s after a still frame):')
for c in cfgs:
    rows = [json.load(open(f)) for f in sorted(glob.glob(f'{d0}/{c}-r*-active-idle.json'))]
    if rows:
        print('%-9s presents %s, our CPU %% %s, WS CPU %% %s' % (c, [d['presents'] for d in rows], [round(d['load']['selfCPUPct'], 2) for d in rows], [round(d['load']['windowServerCPUPct'], 1) for d in rows]))
