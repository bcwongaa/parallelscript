import json
import os
from pathlib import Path
import subprocess
ROOT=Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
ENV=dict(os.environ, GOMAXPROCS='2',PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'),PTS_HOME=os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'),PTS_THREADS='4')
for path in sorted(ROOT.glob('[0-9][0-9]-*.pts')):
    if path.name.startswith('18-') or '-original.' in path.name: continue
    results={}
    for kind in ['pts','node']:
        if kind=='node' and int(path.name[:2]) in [1,11,14,15,16,17,19,20]: continue
        if kind=='pts': cmd=[str(ROOT/'pts'),'run',str(path)]
        else:
            twin=path.with_suffix('.mts'); twin.write_text(path.read_text())
            cmd=[str(Path.home()/'.local/node/bin/node'),'--experimental-strip-types',str(twin)]
        try:
            p=subprocess.run(cmd,env=ENV,capture_output=True,text=True,timeout=90)
            r={'command':cmd,'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
        except subprocess.TimeoutExpired as e:
            r={'command':cmd,'exit':'TIMEOUT','stdout':str(e.stdout),'stderr':str(e.stderr)}
        results[kind]=r
        print(path.name,kind,'exit=',r['exit'],r['stdout'][:1500],r['stderr'][:1600],flush=True)
    if 'node' in results: print('same:',results['pts']['stdout']==results['node']['stdout'],flush=True)
    (ROOT/(path.stem+'-result.json')).write_text(json.dumps(results,indent=2,ensure_ascii=False))
