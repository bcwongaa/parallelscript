"""Make a sequential Node twin (.mts) of a ParallelScript probe.

`.par.map(f)` -> `.map(f)`, `.par.forEach` -> `.forEach`, `parallel for (` -> `for (`,
`parallel e` -> `Promise.resolve(e)` for a call expression on one line, pts:parallel's
range and pts:sync's AtomicInt -> small sequential stand-ins. Usage: twin.py in.pts out.mts
"""
import re
import sys

RANGE = '''function range(start: number, end?: number): number[] & { toArray(): number[] } {
  if (end === undefined) {
    end = start;
    start = 0;
  }
  const items = Array.from({ length: end - start }, (_, i) => start + i) as number[] & { toArray(): number[] };
  items.toArray = () => [...items];
  return items;
}'''

ATOMIC = '''class AtomicInt {
  #value: number;
  constructor(value = 0) {
    this.#value = value;
  }
  add(delta: number): number {
    const old = this.#value;
    this.#value += delta;
    return old;
  }
  load(): number {
    return this.#value;
  }
  store(value: number): void {
    this.#value = value;
  }
}'''


def balanced_call(text: str, start: int) -> int:
    """Index just past the call expression that starts at text[start]."""
    i = start
    while i < len(text) and (text[i].isalnum() or text[i] in "_.$"):
        i += 1
    if i < len(text) and text[i] == "(":
        depth = 0
        while i < len(text):
            if text[i] == "(":
                depth += 1
            elif text[i] == ")":
                depth -= 1
                if depth == 0:
                    return i + 1
            i += 1
    return i


def convert(src: str) -> str:
    src = re.sub(r'^import \{ range \} from "pts:parallel";$', RANGE, src, flags=re.M)
    src = re.sub(r'^import \{ AtomicInt \} from "pts:sync";$', ATOMIC, src, flags=re.M)
    src = src.replace(".par.map(", ".map(").replace(".par.forEach(", ".forEach(")
    src = src.replace("parallel for (", "for (")
    out = []
    i = 0
    for m in re.finditer(r"(?:(?<==> )|(?<=await )|(?<=\()|(?<=, )|(?<== )|(?<=return ))parallel (?=[A-Za-z_])", src):
        if m.start() < i:
            continue
        end = balanced_call(src, m.end())
        out.append(src[i:m.start()])
        out.append("Promise.resolve(" + src[m.end():end] + ")")
        i = end
    out.append(src[i:])
    return "".join(out)


if __name__ == "__main__":
    with open(sys.argv[1]) as f:
        text = f.read()
    with open(sys.argv[2], "w") as f:
        f.write(convert(text))
