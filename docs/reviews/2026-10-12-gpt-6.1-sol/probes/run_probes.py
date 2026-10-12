import json
import os
from pathlib import Path
import resource
import subprocess

ROOT = Path('/tmp/pts-review-progs')
SNAPSHOT = Path('/home/bryan/projects/parallelscript-review')
PTS_ONLY = {'07_integer_edges', '09_par_map', '10_sync_objects', '11_foreign_timer', '15_accessor_stack'}
resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
environment = os.environ.copy()
environment.update(GOMAXPROCS='3', PTS_CACHE_DIR=str(Path.home() / '.cache/pts-review'),
                   PTS_HOME=str(SNAPSHOT), PTS_THREADS='2')


def execute(arguments, timeout):
    try:
        process = subprocess.run(arguments, cwd=SNAPSHOT, env=environment,
                                 text=True, capture_output=True, timeout=timeout)
        return {'exit': process.returncode, 'stdout': process.stdout, 'stderr': process.stderr}
    except subprocess.TimeoutExpired as error:
        return {'exit': 'TIMEOUT', 'stdout': (error.stdout or b'').decode() if isinstance(error.stdout, bytes) else (error.stdout or ''),
                'stderr': (error.stderr or b'').decode() if isinstance(error.stderr, bytes) else (error.stderr or '')}


results = []
for source in sorted(ROOT.glob('*.pts')):
    binary = source.with_suffix('.bin')
    build = execute(['/tmp/pts-review', 'build', str(source), '-o', str(binary)], 90)
    native = execute([str(binary)], 5) if build['exit'] == 0 else None
    node = None
    if source.stem not in PTS_ONLY:
        typescript = source.with_suffix('.ts')
        typescript.write_text(source.read_text())
        node = execute(['node', '--experimental-strip-types', str(typescript)], 5)
    result = {'name': source.stem, 'build': build, 'native': native, 'node': node}
    results.append(result)
    print(json.dumps(result), flush=True)
    (ROOT / 'results.json').write_text(json.dumps(results, indent=2))
