import json
import os
from pathlib import Path
import subprocess

ROOT = Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
ENV = dict(os.environ, GOMAXPROCS='2', PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'), PTS_HOME=os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'), PTS_THREADS='4')
NODE = str(Path.home()/'.local/node/bin/node')

def run(label, args, timeout=120, threads='4'):
    env = dict(ENV, PTS_THREADS=threads)
    try:
        process = subprocess.run(args, env=env, text=True, capture_output=True, timeout=timeout)
        result = {'command': args, 'threads': threads, 'exit': process.returncode, 'stdout': process.stdout, 'stderr': process.stderr}
    except subprocess.TimeoutExpired as error:
        result = {'command': args, 'threads': threads, 'exit': 'TIMEOUT', 'stdout': str(error.stdout), 'stderr': str(error.stderr)}
    (ROOT / (label + '.json')).write_text(json.dumps(result, ensure_ascii=False, indent=2))
    print(label, 'exit=', result['exit'], result['stdout'][:1200], result['stderr'][:2200], flush=True)
    return result

(ROOT/'expenses.csv').write_text('date,category,merchant,amount,tags\n2026-01-01,Food,"Café, Montréal",12.50,lunch\n2026-01-02,food,Café,-1.25,refund\n2026-02-03,Travel,Train,30.00,work; rail\n2026-02-04,Food,Shop,4,\ninvalid,Food,X,2.00,\n2026-02-05,Food,Bad,wat,\n2026-02-06,Food,"Double ""Quote""",7.25,\n')
events=[]
for i in range(777):
    events.append(json.dumps({'timestamp': f'2026-10-{1+i%3:02d}T12:00:00Z', 'site': ['east', 'ouest', 'Café'][i%3], 'sensor': f'sensor-{i%7}', 'value': (i%80)-20, 'unit': 'C' if i%2 else 'F'}, ensure_ascii=False))
events.extend(['{"timestamp":"bad"}', 'null', '{', '{"timestamp":"2026-10-01T00:00:00Z","site":"east","sensor":"a","value":"x","unit":"C"}'])
(ROOT/'events.ndjson').write_text('\n'.join(events)+'\n')

for name in ['ledger', 'task-service', 'telemetry']:
    run(name+'-build-v1', [str(ROOT/'pts'), 'build', str(ROOT/(name+'.pts')), '-o', str(ROOT/name)], timeout=240)
    source=(ROOT/(name+'.pts')).read_text()
    (ROOT/(name+'-v1.pts')).write_text(source)
    if name != 'task-service':
        source=source.replace('"pts:fs"', '"node:fs"').replace('"pts:fs/promises"', '"node:fs/promises"').replace('"pts:path"', '"node:path"').replace('.par.map(', '.map(')
        (ROOT/(name+'.mts')).write_text(source)

if (ROOT/'ledger').exists():
    run('ledger-json', [str(ROOT/'ledger'), str(ROOT/'expenses.csv'), '--json'])
    run('ledger-text', [str(ROOT/'ledger'), str(ROOT/'expenses.csv')])
    run('ledger-args', [str(ROOT/'ledger'), str(ROOT/'expenses.csv'), '--category'])
    run('ledger-node-json', [NODE, '--experimental-strip-types', str(ROOT/'ledger.mts'), str(ROOT/'expenses.csv'), '--json'])
    run('ledger-node-text', [NODE, '--experimental-strip-types', str(ROOT/'ledger.mts'), str(ROOT/'expenses.csv')])
if (ROOT/'task-service').exists():
    for threads in ['1','4']:
        run('task-service-'+threads, [str(ROOT/'task-service')], timeout=30, threads=threads)
if (ROOT/'telemetry').exists():
    for threads in ['1','4']:
        run('telemetry-'+threads, [str(ROOT/'telemetry'), str(ROOT/('report-'+threads+'.json')), str(ROOT/'events.ndjson')], timeout=30, threads=threads)
    run('telemetry-node', [NODE, '--experimental-strip-types', str(ROOT/'telemetry.mts'), str(ROOT/'report-node.json'), str(ROOT/'events.ndjson')])
    for threads in ['1','4']:
        a=ROOT/('report-'+threads+'.json'); b=ROOT/'report-node.json'
        if a.exists() and b.exists(): print('report-'+threads+' byte-identical with Node:',a.read_bytes()==b.read_bytes(),flush=True)
