package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"gotest.tools/v3/assert"
)

func TestReturnedFunctionSourcesJoinAllSynchronousReturns(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
		want []string
	}{
		{"declaration", "function returned(): Fn { return first; } const probe = returned(); probe;", []string{"first"}},
		{"const_aliases", "function returned(): Fn { const alias = first; const again = alias; return again; } const probe = returned(); probe;", []string{"first"}},
		{"arrow_expression", "const returned = (): Fn => first; const probe = returned(); probe;", []string{"first"}},
		{"arrow_block", "const returned = (): Fn => { return first; }; const probe = returned(); probe;", []string{"first"}},
		{"function_expression", "const returned = function(): Fn { return first; }; const probe = returned(); probe;", []string{"first"}},
		{"factory_alias", "function returned(): Fn { return first; } const alias = returned; const again = alias; const probe = again(); probe;", []string{"first"}},
		{"chained_calls", "function returned(): Fn { return first; } function outer(): Fn { return returned(); } const probe = outer(); probe;", []string{"first"}},
		{"multiple_returns", "function returned(flag: boolean): Fn { if (flag) return first; return second; } const probe = returned(true); probe;", []string{"first", "second"}},
		{"conditional_return", "function returned(flag: boolean): Fn { return flag ? first : second; } const probe = returned(true); probe;", []string{"first", "second"}},
		{"finally_returns", "function returned(): Fn { try { return first; } finally { return second; } } const probe = returned(); probe;", []string{"first", "second"}},
		{"catch_returns", "function returned(): Fn { try { return first; } catch { return second; } } const probe = returned(); probe;", []string{"first", "second"}},
		{"switch_returns", "function returned(flag: number): Fn { switch (flag) { case 0: return first; default: return second; } } const probe = returned(0); probe;", []string{"first", "second"}},
		{"loop_returns", "function returned(flag: boolean): Fn { while (flag) return first; return second; } const probe = returned(true); probe;", []string{"first", "second"}},
		{"implicit_undefined", "function returned(flag: boolean): Fn | undefined { if (flag) return first; } const probe = returned(true); probe;", []string{"first"}},
		{"bare_undefined", "function returned(flag: boolean): Fn | undefined { if (flag) return first; return; } const probe = returned(true); probe;", []string{"first"}},
		{"only_undefined", "function returned(): undefined { return; } const probe = returned(); probe;", []string{}},
		{"no_return", "function returned(): void { 1; } const probe = returned(); probe;", []string{}},
		{"closed_parameter", "function returned(f: Fn): Fn { return f; } const probe = returned(first); probe;", []string{"first"}},
		{"shared_parameter", "function returned(f: Fn): Fn { return f; } const probe = returned(first); returned(second); probe;", []string{"first", "second"}},
		{"merged_callees", "function left(): Fn { return first; } function right(): Fn { return second; } const returned = true ? left : right; const probe = returned(); probe;", []string{"first", "second"}},
		{"nested_function_ignored", "function returned(): Fn { function ignored(): Fn { return held[0]; } return first; } const probe = returned(); probe;", []string{"first"}},
		{"nested_arrow_ignored", "function returned(): Fn { const ignored = (): Fn => { return held[0]; }; return first; } const probe = returned(); probe;", []string{"first"}},
		{"nested_method_ignored", "function returned(): Fn { const ignored = { make(): Fn { return held[0]; } }; return first; } const probe = returned(); probe;", []string{"first"}},
		{"nested_class_ignored", "function returned(): Fn { class Ignored { make(): Fn { return held[0]; } } return first; } const probe = returned(); probe;", []string{"first"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, returnedSourcesPrelude+tt.body)
			steps := maxValueFlowSteps

			sources := l.valueSources(probe, &steps, 0)

			assert.Assert(t, sources != nil, "the returned source set must be proven rather than unknown")
			assert.DeepEqual(t, returnedSourceNames(sources), tt.want)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestUncertainReturnedFunctionSourcesStayUnknown(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{"array_read", "function returned(): Fn { return held[0]; } const probe = returned(); probe;"},
		{"mixed_return", "function returned(flag: boolean): Fn { if (flag) return first; return held[0]; } const probe = returned(true); probe;"},
		{"finally_unknown", "function returned(): Fn { try { return first; } finally { return held[0]; } } const probe = returned(); probe;"},
		{"catch_unknown", "function returned(): Fn { try { return first; } catch { return held[0]; } } const probe = returned(); probe;"},
		{"mutable_return", "let selected: Fn = first; function returned(): Fn { return selected; } selected = second; const probe = returned(); probe;"},
		{"assigned_parameter", "function returned(f: Fn): Fn { f = second; return f; } const probe = returned(first); probe;"},
		{"captured_mutation", "function returned(f: Fn): Fn { function replace(): void { f = second; } replace(); return f; } const probe = returned(first); probe;"},
		{"mutable_callee", "let returned: () => Fn = (): Fn => first; returned = (): Fn => second; const probe = returned(); probe;"},
		{"exported_parameter", "export function returned(f: Fn): Fn { return f; } const probe = returned(first); probe;"},
		{"escaped_parameter", "function returned(f: Fn): Fn { return f; } const alias = returned; alias(second); const probe = returned(first); probe;"},
		{"callback_parameter", "function returned(f: Fn): Fn { return f; } [second].map(returned); const probe = returned(first); probe;"},
		{"default_parameter", "function returned(f: Fn = second): Fn { return f; } const probe = returned(first); probe;"},
		{"generic_factory", "function returned<T>(marker: T): Fn { return first; } const probe = returned(1); probe;"},
		{"contextual_generic_arrow", "const returned: <T>(marker: T) => Fn = marker => first; const probe = returned(1); probe;"},
		{"contextual_generic_expression", "const returned: <T>(marker: T) => Fn = function(marker) { return first; }; const probe = returned(1); probe;"},
		{"generic_outer_parameter", "function outer<T>(f: Fn, marker: T): void { function returned(): Fn { return f; } const probe = returned(); probe; } outer(first, 1); outer(second, 's');"},
		{"async_factory", "async function returned(): Promise<Fn> { return first; } const probe = returned(); probe;"},
		{"generator_factory", "function* returned(): Generator<undefined, Fn, unknown> { return first; } const probe = returned(); probe;"},
		{"method_callee", "const object = { returned(): Fn { return first; } }; const probe = object.returned(); probe;"},
		{"recursive_factory", "function returned(depth: number): Fn { if (depth === 0) return first; return returned(depth - 1); } const probe = returned(1); probe;"},
		{"mutual_cycle", "function left(depth: number): Fn { if (depth === 0) return first; return right(depth - 1); } function right(depth: number): Fn { if (depth === 0) return second; return left(depth - 1); } const probe = left(1); probe;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, returnedSourcesPrelude+tt.body)
			steps := maxValueFlowSteps

			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, len(l.valueFlow.active), 0, "failed proofs must clear active query state")
		})
	}
}

func TestReturnSourceWalkerChargesItsBodyWork(t *testing.T) {
	t.Parallel()
	source := returnedSourcesPrelude + "function returned(): Fn {\n" + strings.Repeat("1;\n", 100) + "return first;\n}\nconst probe = returned(); probe;\n"
	l, probe := valueFlowProbe(t, source)
	steps := 12

	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "a long body cannot be proved without charging its visited nodes")
	assert.Equal(t, steps, 0)
}

func TestReturnSourceSummariesAreSharedAcrossDistinctCalls(t *testing.T) {
	t.Parallel()
	source := returnedSourcesPrelude + "function returned(): Fn {\n" + strings.Repeat("1;\n", 100) + "return first;\n}\nconst earlier = returned(); const probe = returned(); probe;\n"
	l, probe := valueFlowProbe(t, source)
	var earlier *ast.Node
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindVariableDeclaration && node.Name().Kind == ast.KindIdentifier && node.Name().Text() == "earlier" {
			earlier = node.Initializer()
		}
		node.ForEachChild(visit)
		return false
	}
	for _, file := range l.valueFlow.files {
		file.AsNode().ForEachChild(visit)
	}
	assert.Assert(t, earlier != nil)
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(earlier, &steps, 0) != nil)
	steps = 8

	sources := l.valueSources(probe, &steps, 0)

	assert.Assert(t, sources != nil, "a distinct call should reuse the declaration's return summary, not walk its long body again")
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
}

func TestReturnSourceDepthLimitStaysUnknown(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	source.WriteString(returnedSourcesPrelude)
	for i := range maxValueFlowDepth + 1 {
		fmt.Fprintf(&source, "function factory%d(): Fn { return factory%d(); }\n", i, i+1)
	}
	fmt.Fprintf(&source, "function factory%d(): Fn { return first; }\nconst probe = factory0(); probe;\n", maxValueFlowDepth+1)
	l, probe := valueFlowProbe(t, source.String())
	steps := maxValueFlowSteps

	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Assert(t, steps > 0, "the depth cap, not total work, must stop this chain")
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func returnedSourceNames(sources functionSources) []string {
	names := make([]string, 0, len(sources))
	for node := range sources {
		names = append(names, node.Name().Text())
	}
	slices.Sort(names)
	return names
}

const returnedSourcesPrelude = `type Fn = () => void;
function first(): void {}
function second(): void {}
const held: Fn[] = [second];
`
