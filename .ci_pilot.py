import re,sys,json
# usage: .ci_pilot.py logfile -> pilot windows per JSON block
t=open(sys.argv[1]).read()
t=re.sub(r'^\d{4}-\d\d-\d\dT[\d:.]+Z ?','',t,flags=re.M)
for m in re.finditer(r'^\{\n.*?^\}\n',t,flags=re.M|re.S):
    try: d=json.loads(m.group(0))
    except Exception: continue
    if 'Windows' in d and 'RowsBuiltPerFrame' in d and 'Rows' in d:
        print(d.get('Rows'), [w['Mallocs'] for w in d['Windows']], d.get('GateFailures'))
