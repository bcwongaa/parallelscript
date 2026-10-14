import json
import os
from pathlib import Path
import subprocess
ROOT=Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
ENV=dict(os.environ,GOMAXPROCS='2',PTS_THREADS='4',PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'),PTS_HOME=os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'))
results={}

def run(label,cmd,threads='4',timeout=180):
    p=subprocess.run(cmd,env=dict(ENV,PTS_THREADS=threads),capture_output=True,text=True,timeout=timeout)
    results[label]={'command':cmd,'threads':threads,'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
    print(label,p.returncode,p.stdout,p.stderr,flush=True)
    return p

for stem in ['04-destructuring','08-generics','12-optional-order','destructuring-undefined-min']:
    path=ROOT/(stem+'.pts')
    run(stem+'-pts',[str(ROOT/'pts'),'run',str(path)])
    twin=path.with_suffix('.mts');twin.write_text(path.read_text())
    run(stem+'-node',[str(Path.home()/'.local/node/bin/node'),'--experimental-strip-types',str(twin)])
run('abort-build',[str(ROOT/'pts'),'build',str(ROOT/'abort-local-min.pts'),'-o',str(ROOT/'abort-local-min')])
for threads in ['1','4','4','4']:
    run('abort-'+threads+'-'+str(len(results)),[str(ROOT/'abort-local-min')],threads)
(ROOT/'recheck-results.json').write_text(json.dumps(results,indent=2))
