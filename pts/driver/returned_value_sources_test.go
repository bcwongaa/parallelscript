package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestReturnedValuesOnlyInstantiateTheirProvenSources(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"declarations", "const_return_aliases", "arrow_expression", "arrow_block",
		"function_expression", "factory_aliases", "chained_calls", "closed_parameters",
		"nested_functions", "nested_methods", "multiple_returns", "conditional_return",
		"finally_returns", "bare_and_implicit_undefined", "merged_callees",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, returnedValueFlowSource(origin))

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			pairs := 0
			merged := origin == "multiple_returns" || origin == "conditional_return" || origin == "finally_returns" || origin == "merged_callees"
			for value := range 3 {
				for demand := range 3 {
					name := fmt.Sprintf("pts_fn_source%d_C%d(", value, demand)
					present := strings.Contains(c, name)
					if present {
						pairs++
					}
					reachable := value == demand || (merged && value == 1 && demand == 0)
					assert.Check(t, present == reachable, "%s: present=%t, required by the source over-approximation=%t", name, present, reachable)
				}
			}
			wantPairs := 3
			if merged {
				wantPairs++
			}
			t.Logf("3 returned values x 3 demands: %d named pairs; want %d source-permitted pairs", pairs, wantPairs)
		})
	}
}

func returnedValueFlowSource(origin string) string {
	var source strings.Builder
	source.WriteString(`class Box<T> { constructor(public value: T) {} }
type Factory = <T>(value: T) => Box<T>;
function choose(): boolean { return true; }
`)
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "function source%d<T>(value: T): Box<T> { return new Box(value); }\n", i)
		fmt.Fprintf(&source, "function use%d(f: Factory): void { const box: Box<C%d> = f(new C%d()); console.log(box.value.tag); }\n", i, i, i)
	}
	for i := range 3 {
		switch origin {
		case "const_return_aliases":
			fmt.Fprintf(&source, "function factory%d(): Factory { const alias = source%d; const again = alias; return again; }\n", i, i)
		case "arrow_expression":
			fmt.Fprintf(&source, "const factory%d = (): Factory => source%d;\n", i, i)
		case "arrow_block":
			fmt.Fprintf(&source, "const factory%d = (): Factory => { return source%d; };\n", i, i)
		case "function_expression":
			fmt.Fprintf(&source, "const factory%d = function(): Factory { return source%d; };\n", i, i)
		case "factory_aliases":
			fmt.Fprintf(&source, "function original%d(): Factory { return source%d; }\nconst alias%d = original%d; const factory%d = alias%d;\n", i, i, i, i, i, i)
		case "chained_calls":
			fmt.Fprintf(&source, "function original%d(): Factory { return source%d; }\nfunction factory%d(): Factory { return original%d(); }\n", i, i, i, i)
		case "closed_parameters":
			fmt.Fprintf(&source, "function factory%d(f: Factory): Factory { return f; }\n", i)
		case "nested_functions":
			fmt.Fprintf(&source, "function factory%d(): Factory { const unknown: Factory[] = [source%d]; function ignored(): Factory { return unknown[0]; } return source%d; }\n", i, i, i)
		case "nested_methods":
			fmt.Fprintf(&source, "function factory%d(): Factory { const unknown: Factory[] = [source%d]; const ignored = { make(): Factory { return unknown[0]; } }; return source%d; }\n", i, i, i)
		case "bare_and_implicit_undefined":
			if i == 0 {
				fmt.Fprintf(&source, "function factory%d(flag: boolean): Factory | undefined { if (flag) return source%d; return; }\n", i, i)
			} else {
				fmt.Fprintf(&source, "function factory%d(flag: boolean): Factory | undefined { if (flag) return source%d; }\n", i, i)
			}
		default:
			if i == 0 {
				switch origin {
				case "multiple_returns":
					source.WriteString("function factory0(): Factory { if (choose()) return source0; return source1; }\n")
					continue
				case "conditional_return":
					source.WriteString("function factory0(): Factory { return choose() ? source0 : source1; }\n")
					continue
				case "finally_returns":
					source.WriteString("function factory0(): Factory { try { return source0; } finally { return source1; } }\n")
					continue
				}
			}
			fmt.Fprintf(&source, "function factory%d(): Factory { return source%d; }\n", i, i)
		}
	}
	for i := range 3 {
		argument := fmt.Sprintf("factory%d()", i)
		if origin == "closed_parameters" {
			argument = fmt.Sprintf("factory%d(source%d)", i, i)
		}
		if origin == "bare_and_implicit_undefined" {
			argument = fmt.Sprintf("factory%d(choose())!", i)
		}
		if origin == "merged_callees" && i == 0 {
			source.WriteString("const selected = choose() ? factory0 : factory1;\n")
			argument = "selected()"
		}
		fmt.Fprintf(&source, "const returned%d = %s; use%d(returned%d);\n", i, argument, i, i)
	}
	return source.String()
}

func TestUnknownReturnedValuesKeepEveryCompatibleInstance(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{"any_readback", "function returned(): Factory { return held[1]; } use(returned());"},
		{"unknown_readback", "const unknown: unknown[] = held; function returned(): Factory { return unknown[1] as Factory; } use(returned());"},
		{"mixed_known_unknown", "function returned(flag: boolean): Factory { if (flag) return first; return held[1]; } use(returned(false));"},
		{"unknown_callee", "function returned(): Factory { return second; } const callees: any[] = [returned]; const dynamic: any = callees[0]; const f: Factory = dynamic(); use(f);"},
		{"mutable_return", "let selected: Factory = first; function returned(): Factory { return selected; } selected = second; use(returned());"},
		{"assigned_parameter", "function returned(f: Factory): Factory { f = second; return f; } use(returned(first));"},
		{"captured_mutation", "function returned(f: Factory): Factory { function replace(): void { f = second; } replace(); return f; } use(returned(first));"},
		{"mutable_factory", "let returned: () => Factory = (): Factory => first; returned = (): Factory => second; use(returned());"},
		{"escaped_factory_parameter", "function returned(f: Factory): Factory { return f; } returned(first); const alias = returned; use(alias(second));"},
		{"callback_factory_parameter", "function returned(f: Factory): Factory { return f; } const mapped = [second].map(returned); use(returned(first)); use(mapped[0]);"},
		{"exported_factory_parameter", "export function returned(f: Factory): Factory { return f; } use(returned(second));"},
		{"any_parameter", "function returned(f: any): Factory { return f; } use(returned(held[1]));"},
		{"generic_factory", "function returned<U>(f: Factory, marker: U): Factory { return f; } use(returned(second, 0));"},
		{"contextual_generic_arrow", "const returned: <U>(marker: U) => Factory = marker => second; use(returned(0));"},
		{"contextual_generic_expression", "const returned: <U>(marker: U) => Factory = function(marker) { return second; }; use(returned(0));"},
		{"generic_enclosing_instances", "function enclosing<U>(f: Factory, marker: U): void { function returned(): Factory { return f; } use(returned()); } enclosing(first, 0); enclosing(second, 's');"},
		{"recursive_factory", "function returned(f: Factory, depth: number): Factory { if (depth > 0) return returned(f, depth - 1); return f; } use(returned(second, 1));"},
		{"mutually_recursive_factories", "function left(depth: number): Factory { if (depth === 0) return first; return right(depth - 1); } function right(depth: number): Factory { if (depth === 0) return second; return left(depth - 1); } use(left(1));"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
type Factory = <T>(value: T) => Box<T>;
const first = <T,>(value: T): Box<T> => new Box(value);
const second = <T,>(value: T): Box<T> => new Box(value);
const third = <T,>(value: T): Box<T> => new Box(value);
const held: any[] = [first, second, third];
function use(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
`+tt.body+"\n")

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for _, name := range []string{"pts_fn_first_C0(", "pts_fn_second_C0(", "pts_fn_third_C0("} {
				assert.Check(t, strings.Contains(c, name), "%s must remain when returned sources are unknown", name)
			}
		})
	}
}

func TestSharedFactoryParameterSummaryIncludesEveryCaller(t *testing.T) {
	t.Parallel()
	entry := writeSource(t, `class Box<T> { constructor(public value: T) {} }
class C0 { tag = 0; }
class C1 { tag = 1; }
type Factory = <T>(value: T) => Box<T>;
function first<T>(value: T): Box<T> { return new Box(value); }
function second<T>(value: T): Box<T> { return new Box(value); }
function unused<T>(value: T): Box<T> { return new Box(value); }
const held: any[] = [unused];
function returned(f: Factory): Factory { return f; }
function use0(f: Factory): void { const box: Box<C0> = f(new C0()); console.log(box.value.tag); }
function use1(f: Factory): void { const box: Box<C1> = f(new C1()); console.log(box.value.tag); }
use0(returned(first));
use1(returned(second));
console.log(held.length);
`)

	c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

	assert.NilError(t, err)
	for demand := range 2 {
		for _, value := range []string{"first", "second"} {
			name := fmt.Sprintf("pts_fn_%s_C%d(", value, demand)
			assert.Check(t, strings.Contains(c, name), "%s must remain: a declaration-cached parameter summary joins all callers", name)
		}
		name := fmt.Sprintf("pts_fn_unused_C%d(", demand)
		assert.Check(t, !strings.Contains(c, name), "%s cannot reach either known factory return", name)
	}
}
