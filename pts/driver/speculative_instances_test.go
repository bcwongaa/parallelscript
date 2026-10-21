package driver_test

// H09b5's RED test for C172(c): specialize pairs every value demand with every erased value of
// its erased function type (D-148, D-190), so code grows with escaping values x demand types.
// Use AllInstances directly so the driver's fallback cannot hide a rejected flow lowering.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestValuesThatNoTypedCallReachesGetNoInstances(t *testing.T) {
	t.Parallel()
	// a0, a1 and a2 escape into an any[] that nothing reads back, so no typed call can reach
	// them; use's two demands (Box<C0>, Box<C1>) reach only the arrow passed to it. The base
	// made an instance of each escaping arrow for each demand type: 2 x 3 that no call uses
	// (review round 2's gen_escape.py; 150 arrows x 50 types made 92,388 lines of C).
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class C0 {
  tag = 0;
}
class C1 {
  tag = 1;
}
let total = 0;
const keep: any[] = [];
function use(f: <T>(x: T) => Box<T>): void {
  const b0: Box<C0> = f(new C0());
  total += b0.value.tag;
  const b1: Box<C1> = f(new C1());
  total += b1.value.tag;
}
function main(): void {
  const a0 = <T>(x: T): Box<T> => new Box(x);
  keep.push(a0);
  const a1 = <T>(x: T): Box<T> => new Box(x);
  keep.push(a1);
  const a2 = <T>(x: T): Box<T> => new Box(x);
  keep.push(a2);
  use(<T>(x: T): Box<T> => new Box(x));
}
main();
console.log(total, keep.length);
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(c, "pts_fn_main_arrow_C0") && strings.Contains(c, "pts_fn_main_arrow_C1"), "the arrow passed to use has its instances")
	for value := range 3 {
		for demand := range 2 {
			unused := fmt.Sprintf("pts_fn_main_a%d_C%d", value, demand)
			assert.Check(t, !strings.Contains(c, unused), "%s is an instance no call can use", unused)
		}
	}
}

func TestClosedValueDemandsOnlyInstantiateTheirKnownSources(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"arrows", "declarations", "const_aliases", "parameter_forwarding", "conditional", "comma", "or", "nullish", "and"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			const size = 3
			entry := writeSource(t, closedValueFlowSource(size, origin))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			prefix := "pts_fn_source"
			if origin != "declarations" {
				prefix = "pts_fn_main_source"
			}
			merged := origin == "conditional" || origin == "or" || origin == "nullish" || origin == "and"
			wantPairs := size
			if merged {
				wantPairs++
			}
			pairs := 0
			for value := range size {
				for demand := range size {
					name := fmt.Sprintf("%s%d_C%d(", prefix, value, demand)
					present := strings.Contains(c, name)
					if present {
						pairs++
					}
					reachable := value == demand || (merged && value == 1 && demand == 0)
					assert.Check(t, present == reachable, "%s: present=%t, reachable=%t", name, present, reachable)
				}
			}
			t.Logf("%d values x %d demands: %d named instance pairs; want %d reachable pairs", size, size, pairs, wantPairs)
		})
	}
}

func TestUnknownConsumersKeepCompatibleValues(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		prefix string
		uses   string
	}{
		{"exported_declaration", "export ", "consume(first);"},
		{"standalone_export", "", "consume(first); export { consume };"},
		{"renamed_export", "", "consume(first); export { consume as accept };"},
		{"shorthand_reference", "", "const held = { consume }; console.log(held.consume === held.consume); consume(first);"},
		{"shorthand_default", "", "consume(first); let held: (f: Factory) => void = (_f: Factory): void => {}; const options: { held?: (f: Factory) => void } = {}; ({ held = consume } = options); held(first);"},
		{"aliased_reference", "", "const held = consume; held(first);"},
		{"callback_reference", "", "[first].forEach(consume);"},
		{"any_reference", "", "const held: any = consume; held(first);"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// second is never passed locally, but an escaped/exported consumer is not closed.
			// A source proof must not remove compatible instances merely from local callers.
			entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class C0 { tag = 0; }
type Factory = <T>(value: T) => Box<T>;
const first = <T>(x: T): Box<T> => new Box(x);
const second = <T>(x: T): Box<T> => new Box(x);
const keep: any[] = [second];
`+tt.prefix+`function consume(f: Factory): void {
  const box: Box<C0> = f(new C0());
  console.log(box.value.tag);
}
`+tt.uses+`console.log(keep.length);
`)

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for _, name := range []string{"pts_fn_first_C0(", "pts_fn_second_C0("} {
				assert.Check(t, strings.Contains(c, name), "%s must remain for an unknown consumer", name)
			}
		})
	}
}

func closedValueFlowSource(size int, origin string) string {
	var source strings.Builder
	source.WriteString(`class Box<T> {
  constructor(public value: T) {}
}
type Factory = <T>(value: T) => Box<T>;
`)
	for i := range size {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "function use%d(f: Factory): void { const b: Box<C%d> = f(new C%d()); console.log(b.value.tag); }\n", i, i, i)
		if origin == "declarations" {
			fmt.Fprintf(&source, "function source%d<T>(x: T): Box<T> { return new Box(x); }\n", i)
		}
		if origin == "parameter_forwarding" {
			fmt.Fprintf(&source, "function relay%d(f: Factory): void { use%d(f); }\n", i, i)
		}
	}
	source.WriteString("function choose(): boolean { return true; }\nfunction main(): void {\n")
	if origin != "declarations" {
		for i := range size {
			fmt.Fprintf(&source, "  const source%d = <T>(x: T): Box<T> => new Box(x);\n", i)
		}
	}
	if origin == "conditional" || origin == "or" || origin == "nullish" || origin == "and" {
		source.WriteString("  const flag = choose();\n  const maybe: Factory | undefined = flag ? source0 : undefined;\n")
	}
	for i := range size {
		if i == 0 {
			switch origin {
			case "conditional":
				source.WriteString("  use0(flag ? source0 : source1);\n")
				continue
			case "or":
				source.WriteString("  use0(maybe || source1);\n")
				continue
			case "nullish":
				source.WriteString("  use0(maybe ?? source1);\n")
				continue
			case "and":
				source.WriteString("  use0((maybe && source1) || source0);\n")
				continue
			}
		}
		switch origin {
		case "comma":
			fmt.Fprintf(&source, "  use%d((console.log(\"comma\"), source%d));\n", i, i)
		case "const_aliases":
			fmt.Fprintf(&source, "  const alias%d = source%d; const again%d = alias%d; use%d(again%d);\n", i, i, i, i, i, i)
		case "parameter_forwarding":
			fmt.Fprintf(&source, "  relay%d(source%d);\n", i, i)
		default:
			fmt.Fprintf(&source, "  use%d(source%d);\n", i, i)
		}
	}
	source.WriteString("}\nmain();\n")
	return source.String()
}
