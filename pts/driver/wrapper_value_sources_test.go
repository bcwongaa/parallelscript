package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestMethodWrapperDemandsOnlyInstantiateTheirProvenCreations(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"direct", "inline", "aliases", "closed_parameters", "conditional", "logical",
		"returned", "returned_aliases", "merged_returns",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, methodWrapperFlowSource(origin))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			merged := origin == "conditional" || origin == "logical" || origin == "merged_returns"
			assertWrapperTargetPairs(t, c, "pts_fn_M%d_make_C%d(", merged)
		})
	}
}

func methodWrapperFlowSource(origin string) string {
	var source strings.Builder
	source.WriteString(`class Box<T> { constructor(public value: T) {} }
type Factory = <T>(value: T) => Box<T>;
function choose(): boolean { return true; }
`)
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "class M%d { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
		fmt.Fprintf(&source, "function use%d(f: Factory): void { const box: Box<C%d> = f(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		if origin == "closed_parameters" {
			fmt.Fprintf(&source, "function relay%d(f: Factory): void { use%d(f); }\n", i, i)
		}
		if origin == "returned" || origin == "returned_aliases" || origin == "merged_returns" {
			if origin == "merged_returns" && i == 0 {
				source.WriteString("function read0(): Factory { if (choose()) return new M0().make; return new M1().make; }\n")
			} else {
				fmt.Fprintf(&source, "function read%d(): Factory { return new M%d().make; }\n", i, i)
			}
		}
	}
	source.WriteString("function main(): void {\n")
	for i := range 3 {
		switch origin {
		case "returned", "merged_returns":
			fmt.Fprintf(&source, "const method%d = read%d();\n", i, i)
		case "returned_aliases":
			fmt.Fprintf(&source, "const readAlias%d = read%d; const method%d = readAlias%d();\n", i, i, i, i)
		default:
			fmt.Fprintf(&source, "const method%d = new M%d().make;\n", i, i)
		}
	}
	if origin == "conditional" || origin == "logical" {
		source.WriteString("const flag = choose(); const maybe: Factory | undefined = flag ? method0 : undefined;\n")
	}
	for i := range 3 {
		if i == 0 && origin == "conditional" {
			source.WriteString("use0(flag ? method0 : method1);\n")
			continue
		}
		if i == 0 && origin == "logical" {
			source.WriteString("use0(maybe || method1);\n")
			continue
		}
		switch origin {
		case "inline":
			fmt.Fprintf(&source, "use%d(new M%d().make);\n", i, i)
		case "aliases":
			fmt.Fprintf(&source, "const alias%d = method%d; const again%d = alias%d; use%d(again%d);\n", i, i, i, i, i, i)
		case "closed_parameters":
			fmt.Fprintf(&source, "relay%d(method%d);\n", i, i)
		default:
			fmt.Fprintf(&source, "use%d(method%d);\n", i, i)
		}
	}
	source.WriteString("}\nmain();\n")
	return source.String()
}

func TestBoundAndAdaptedDemandsOnlyInstantiateTheirProvenTargets(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"zero_bind", "bind_alias", "bind_chain", "bound_return", "nongeneric_argument",
		"typed_const", "instantiation_expression", "array_mapper",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, boundAndAdaptedFlowSource(origin))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			assertWrapperTargetPairs(t, c, "pts_fn_main_source%d_C%d(", false)
		})
	}
}

func boundAndAdaptedFlowSource(origin string) string {
	var source strings.Builder
	source.WriteString(`class Box<T> { constructor(public value: T) {} }
type Factory = <T>(value: T) => Box<T>;
`)
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		if origin == "nongeneric_argument" {
			fmt.Fprintf(&source, "function use%d(f: (value: C%d) => Box<C%d>): void { console.log(f(new C%d()).value.tag); }\n", i, i, i, i)
		} else if origin != "typed_const" && origin != "instantiation_expression" && origin != "array_mapper" {
			fmt.Fprintf(&source, "function use%d(f: Factory): void { const box: Box<C%d> = f(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		}
	}
	source.WriteString("function main(): void {\n")
	for i := range 3 {
		fmt.Fprintf(&source, "const source%d = <T,>(value: T): Box<T> => new Box(value);\n", i)
	}
	for i := range 3 {
		switch origin {
		case "zero_bind":
			fmt.Fprintf(&source, "use%d(source%d.bind(undefined));\n", i, i)
		case "bind_alias":
			fmt.Fprintf(&source, "const bound%d = source%d.bind(undefined); const alias%d = bound%d; use%d(alias%d);\n", i, i, i, i, i, i)
		case "bind_chain":
			fmt.Fprintf(&source, "const bound%d = source%d.bind(undefined).bind(undefined); use%d(bound%d);\n", i, i, i, i)
		case "bound_return":
			fmt.Fprintf(&source, "function returned%d(): Factory { return source%d.bind(undefined); } use%d(returned%d());\n", i, i, i, i)
		case "nongeneric_argument":
			fmt.Fprintf(&source, "use%d(source%d);\n", i, i)
		case "typed_const":
			fmt.Fprintf(&source, "const adapted%d: (value: C%d) => Box<C%d> = source%d; console.log(adapted%d(new C%d()).value.tag);\n", i, i, i, i, i, i)
		case "instantiation_expression":
			fmt.Fprintf(&source, "const adapted%d = source%d<C%d>; console.log(adapted%d(new C%d()).value.tag);\n", i, i, i, i, i)
		case "array_mapper":
			fmt.Fprintf(&source, "const boxes%d: Box<C%d>[] = [new C%d()].map(source%d); console.log(boxes%d[0].value.tag);\n", i, i, i, i, i)
		}
	}
	source.WriteString("}\nmain();\n")
	return source.String()
}

func assertWrapperTargetPairs(t *testing.T, c string, format string, merged bool) {
	t.Helper()
	pairs := 0
	for value := range 3 {
		for demand := range 3 {
			name := fmt.Sprintf(format, value, demand)
			present := strings.Contains(c, name)
			if present {
				pairs++
			}
			reachable := value == demand || merged && value == 1 && demand == 0
			assert.Check(t, present == reachable, "%s: present=%t, required by the source over-approximation=%t", name, present, reachable)
		}
	}
	want := 3
	if merged {
		want++
	}
	t.Logf("3 targets x 3 demand signatures: %d pairs; want %d required source-permitted pairs", pairs, want)
}

func TestMethodFieldForwardersRetainOnlyTheirCapturedFunctionSources(t *testing.T) {
	t.Parallel()
	for _, property := range []string{"make: source0", "make"} {
		t.Run(property, func(t *testing.T) {
			t.Parallel()
			name := "source0"
			if property == "make" {
				name = "make"
			}
			entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
class C1 { tag = 1; }
interface Maker { make<T>(value: T): Box<T>; }
const source0 = <T,>(value: T): Box<T> => new Box(value);
const source1 = <T,>(value: T): Box<T> => new Box(value);
const source2 = <T,>(value: T): Box<T> => new Box(value);
const make = source0;
const held: any[] = [source1, source2];
const maker: Maker = { `+property+` };
const first: Box<C0> = maker.make(new C0());
const second: Box<C1> = maker.make(new C1());
console.log(first.value.tag, second.value.tag, held.length);
`)

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for demand := range 2 {
				assert.Check(t, strings.Contains(c, fmt.Sprintf("pts_fn_source0_C%d(", demand)), "%s's captured source needs C%d", name, demand)
				for unused := 1; unused < 3; unused++ {
					assert.Check(t, !strings.Contains(c, fmt.Sprintf("pts_fn_source%d_C%d(", unused, demand)), "the synthetic demand cannot call uncaptured source%d", unused)
				}
			}
		})
	}
}

func TestUnknownWrapperOriginsKeepCompatibleTargets(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{"any_readback", "const held: any[] = [bound0, bound1, bound2]; use(held[1]);"},
		{"unknown_readback", "const held: unknown[] = [bound0, bound1, bound2]; use(held[1] as Factory);"},
		{"mixed_known_unknown", "const held: any[] = [bound1]; use(choose() ? bound0 : held[0]);"},
		{"assigned_binding", "let chosen: Factory = bound0; chosen = bound1; use(chosen);"},
		{"captured_assignment", "let chosen: Factory = bound0; function change(): void { chosen = bound1; } change(); use(chosen);"},
		{"escaped_consumer", "const alias = use; alias(bound1);"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
type Factory = <T>(value: T) => Box<T>;
function choose(): boolean { return false; }
function source0<T>(value: T): Box<T> { return new Box(value); }
function source1<T>(value: T): Box<T> { return new Box(value); }
function source2<T>(value: T): Box<T> { return new Box(value); }
const bound0 = source0.bind(undefined);
const bound1 = source1.bind(undefined);
const bound2 = source2.bind(undefined);
function use(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
`+tt.body+"\n")

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for value := range 3 {
				name := fmt.Sprintf("pts_fn_source%d_C0(", value)
				assert.Check(t, strings.Contains(c, name), "%s must remain for an unproven wrapper origin", name)
			}
		})
	}
}
