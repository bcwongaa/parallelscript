#!/bin/bash
# tonode.sh in.pts out.ts: rewrite a ParallelScript program for Node, with sequential stand-ins
# for pts:parallel and pts:sync (tools/nodeshim) and node: for pts:fs, pts:path, pts:process.
shim=$(cd "$(dirname "$0")/nodeshim" && pwd)
sed -e "s#\"pts:parallel\"#\"$shim/parallel.ts\"#; s#\"pts:sync\"#\"$shim/sync.ts\"#" \
    -e 's#"pts:\(fs\|path\|process\|fs/promises\)"#"node:\1"#' \
    -e 's/parallel for (/for (/g; s/\bparallel \([a-zA-Z(]\)/\1/g; s/ as int\b//g; s/: int\b/: number/g' "$1" > "$2"
grep -q "$shim/parallel.ts" "$2" || sed -i "1i import \"$shim/parallel.ts\";" "$2"
