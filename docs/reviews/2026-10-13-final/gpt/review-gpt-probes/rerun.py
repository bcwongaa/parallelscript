"""Rerun in a fresh scratch directory; retained review evidence is never overwritten."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

SOURCE = Path(__file__).resolve().parent
REPO = SOURCE.parent
BASE = Path('/tmp/pts-review-gpt')
BASE.mkdir(exist_ok=True)
SCRATCH = Path(tempfile.mkdtemp(prefix='rerun-', dir=BASE))
for path in SOURCE.iterdir():
    if path.is_file() and path.suffix in {'.pts', '.mts', '.mjs', '.py', '.csv', '.ndjson'}:
        shutil.copy2(path, SCRATCH/path.name)
for name in ['previous', 'excluded-corpus']:
    directory = SOURCE/name
    if directory.is_dir():
        shutil.copytree(directory, SCRATCH/name)
ENV = dict(os.environ, GOMAXPROCS='2', PTS_THREADS='4',
           PTS_HOME=str(REPO), PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'),
           PTS_REVIEW_SCRATCH=str(SCRATCH))
NODE = str(Path.home()/'.local/node/bin/node')
RESULTS = {}

def run(label, command, threads='4', timeout=240):
    print(label, ' '.join(command), flush=True)
    try:
        p = subprocess.run(command, env=dict(ENV, PTS_THREADS=threads), cwd=REPO,
                           capture_output=True, text=True, timeout=timeout)
        result = {'command':command, 'threads':threads, 'exit':p.returncode,
                  'stdout':p.stdout, 'stderr':p.stderr}
    except subprocess.TimeoutExpired as error:
        result = {'command':command, 'threads':threads, 'exit':'TIMEOUT',
                  'stdout':str(error.stdout), 'stderr':str(error.stderr)}
    RESULTS[label] = result
    (SCRATCH/(label+'.json')).write_text(json.dumps(result, indent=2))
    print('exit',result['exit'],result['stdout'],result['stderr'],flush=True)
    return result

print('Fresh scratch:', SCRATCH, flush=True)
build = run('compiler-build', ['go','build','-p','1','-o',str(SCRATCH/'pts'),'./cmd/pts'], timeout=2400)
if build['exit'] != 0:
    raise SystemExit(1)
PTS = str(SCRATCH/'pts')
for name in ['ledger','task-service','telemetry']:
    run(name+'-build', [PTS,'build',str(SCRATCH/(name+'.pts')),'-o',str(SCRATCH/name)])
for kind,command in [('pts',[str(SCRATCH/'ledger')]),('node',[NODE,'--experimental-strip-types',str(SCRATCH/'ledger.mts')])]:
    run('ledger-'+kind, command+[str(SCRATCH/'expenses.csv'),'--json'])
for threads in ['1','4']:
    run('service-'+threads, [str(SCRATCH/'task-service')], threads, timeout=60)
    run('telemetry-'+threads, [str(SCRATCH/'telemetry'),str(SCRATCH/('report-'+threads+'.json')),str(SCRATCH/'events.ndjson')],threads,timeout=60)
run('telemetry-node',[NODE,'--experimental-strip-types',str(SCRATCH/'telemetry.mts'),str(SCRATCH/'report-node.json'),str(SCRATCH/'events.ndjson')])
for threads in ['1','4']:
    print('telemetry',threads,'byte-identical:',(SCRATCH/('report-'+threads+'.json')).read_bytes()==(SCRATCH/'report-node.json').read_bytes(),flush=True)
for script in ['probe-run.py','recheck.py','http-wire.py','http-framing-compare.py','lsp-smoke.py','check-excluded-corpus.py','previous/run_originals.py']:
    run(Path(script).stem, ['python3','-I',str(SCRATCH/script)], timeout=2400)
for name in ['06_residual','07_residual','08_residual']:
    run(name+'-pts', [PTS,'run',str(SCRATCH/'previous'/(name+'.pts'))], threads='2')
    run(name+'-node', [NODE,'--experimental-strip-types',str(SCRATCH/'previous'/(name+'.ts'))], threads='2')
run('known-lru-pts', [PTS,'run',str(SCRATCH/'known-lru.pts')], threads='2')
run('known-lru-node', [NODE,'--experimental-transform-types','--no-warnings',str(SCRATCH/'known-lru.mts')], threads='2')
run('guide-tutorial',[PTS,'run','docs/examples/guide_tutorial.pts'])
run('guide-tests',[PTS,'test','docs/examples/testing'])
run('expected-test-failure',[PTS,'test',str(SCRATCH/'review-failure.test.pts')])
(SCRATCH/'rerun-summary.json').write_text(json.dumps(RESULTS,indent=2))
print('All results retained at',SCRATCH,flush=True)
print('Some probes intentionally reproduce defects; an exit-0 harness is not a clean verdict.',flush=True)
