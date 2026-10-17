"""Summarize probe logs: the result lines (prefix regex) and the other lines' counts.

Usage: summ.py <result-line-regex> log...
"""
import collections
import re
import sys

pattern = re.compile(sys.argv[1])
for path in sys.argv[2:]:
    print("==", path)
    other = collections.Counter()
    with open(path, errors="replace") as f:
        for line in f:
            line = line.rstrip("\n")
            if pattern.match(line):
                print("  " + line[:300])
            elif line.strip() and not line.startswith("    at "):
                other[line[:160]] += 1
    for line, n in other.most_common(6):
        print(f"  [{n}x] {line}")
