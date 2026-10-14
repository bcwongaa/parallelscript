import json
import os
from pathlib import Path
import resource
import subprocess

CHECKOUT = Path(os.environ.get('PTS_HOME', '/home/bryan/projects/pts-review-gpt'))
ROOT = Path(os.environ.get('PTS_REVIEW_SCRATCH', '/tmp/pts-review-gpt'))
SCRATCH = ROOT / 'previous'
COMPILER = ROOT / 'pts'
NODE = Path.home() / '.local/node/bin/node'
ORIGINALS = [
    '11_foreign_timer', '01_union_capture', '12_captured_tdz',
    '14_tostring_dynamic', '05_thenable_getters', '06_yield_order',
    '13_iterator_protocol', '03_generics_any', '15_accessor_stack', '09_par_map',
]
CURRENT_TESTS = ['generic_factories_fallbacks', 'async_generators_foreign', 'remote_rejection']
resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
environment = os.environ.copy()
environment.update(GOMAXPROCS='2', PTS_CACHE_DIR=str(Path.home() / '.cache/pts-review-gpt'),
                   PTS_HOME=str(CHECKOUT), PTS_THREADS='2')


def run_command(arguments, timeout):
    try:
        process = subprocess.run([str(arg) for arg in arguments], cwd=CHECKOUT,
                                 env=environment, text=True, capture_output=True,
                                 timeout=timeout)
        return {'exit': process.returncode, 'stdout': process.stdout, 'stderr': process.stderr}
    except subprocess.TimeoutExpired as error:
        def text(value):
            return value.decode() if isinstance(value, bytes) else (value or '')
        return {'exit': 'TIMEOUT', 'stdout': text(error.stdout), 'stderr': text(error.stderr)}


def run_probe(name, source, typescript):
    binary = SCRATCH / (name + '.bin')
    build = run_command([COMPILER, 'build', source, '-o', binary], 90)
    native = run_command([binary], 5) if build['exit'] == 0 else None
    node = run_command([NODE, '--experimental-strip-types', typescript], 5) if typescript else None
    return {'name': name, 'source': str(source), 'build': build, 'native': native, 'node': node}


def save_results(results):
    (SCRATCH / 'current_results.json').write_text(json.dumps(results, indent=2) + '\n')


results = []
for name in ORIGINALS:
    typescript = SCRATCH / (name + '.ts')
    # The original accessor TS twin excludes ParallelScript's operator on purpose.
    node_source = typescript if typescript.exists() and name != '15_accessor_stack' else None
    result = run_probe(name, SCRATCH / (name + '.pts'), node_source)
    results.append(result)
    save_results(results)
    print(json.dumps(result), flush=True)

for name in CURRENT_TESTS:
    source = CHECKOUT / 'pts/tests/run' / (name + '.pts')
    result = run_probe(name, source, None)
    results.append(result)
    save_results(results)
    print(json.dumps(result), flush=True)
