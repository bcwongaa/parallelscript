import json, os, shutil, subprocess
from pathlib import Path
ROOT=Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
HOME=Path(os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'))
ENV=dict(os.environ,GOMAXPROCS='2',PTS_THREADS='4',PTS_HOME=str(HOME),PTS_CACHE_DIR=str(Path.home()/'.cache/pts-review-gpt'))
NAMES=['functional/debounce_throttle_clock','algorithms/lcs_diff','text/word_search_puzzle','text/spell_suggest','oop/memento_game_save']
DIR=ROOT/'excluded-corpus';DIR.mkdir(exist_ok=True)
results={}
for name in NAMES:
 source=HOME/'pts/tests/corpus'/(name+'.pts');copy=DIR/(Path(name).name+'.pts');shutil.copy2(source,copy);twin=copy.with_suffix('.mts');twin.write_text(source.read_text())
 results[name]={}
 commands={'pts':[str(ROOT/'pts'),'run',str(copy)],'node':[str(Path.home()/'.local/node/bin/node'),'--experimental-strip-types',str(twin)]}
 for kind,cmd in commands.items():
  p=subprocess.run(cmd,env=ENV,capture_output=True,text=True,timeout=120)
  results[name][kind]={'command':cmd,'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
  if kind=='node' and 'ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX' in p.stderr:
   cmd=[str(Path.home()/'.local/node/bin/node'),'--experimental-transform-types','--no-warnings',str(twin)]
   p=subprocess.run(cmd,env=ENV,capture_output=True,text=True,timeout=120)
   results[name]['node-transformed']={'command':cmd,'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
  print(name,kind,p.returncode,p.stdout[:600],p.stderr[:400],flush=True)
 pts=results[name]['pts'];node=results[name].get('node-transformed',results[name]['node'])
 print('same:',pts['stdout']==node['stdout'] and pts['exit']==node['exit'],flush=True)
(ROOT/'excluded-corpus-results.json').write_text(json.dumps(results,indent=2))
