package lower

import (
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestReturnSourcesChargeCachedCalleeJoins(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, returnedSourcesPrelude+`
function factory0(): Fn { return first; }
function factory1(): Fn { return first; }
function factory2(): Fn { return first; }
function factory3(): Fn { return first; }
function choose(): boolean { return true; }
const selected = choose() ? factory0 : choose() ? factory1 : choose() ? factory2 : factory3;
const probe = selected();
probe;
`)
	call := l.target(probe).ValueDeclaration.Initializer().AsCallExpression()
	steps := maxValueFlowSteps
	callees := l.valueSources(call.Expression, &steps, 0)
	assert.Equal(t, len(callees), 4)
	for callee := range callees {
		steps = maxValueFlowSteps
		sources := l.returnSources(callee, &steps, 0)
		assert.Assert(t, sources != nil)
		assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	}

	steps = 3
	assert.Assert(t, l.callSources(call, &steps, 0) == nil, "joining four cached callee summaries must exhaust three steps, even though all four return the same source")
	assert.Equal(t, steps, 0)
	assert.Equal(t, len(l.valueFlow.active), 0)

	steps = 4
	sources := l.callSources(call, &steps, 0)
	assert.Assert(t, sources != nil, "four cached joins fit exactly four steps")
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	assert.Equal(t, steps, 0)
}

func TestReturnedSourceUnionLimit(t *testing.T) {
	t.Parallel()
	for _, count := range []int{maxFunctionSources, maxFunctionSources + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			source.WriteString("type Fn = () => void;\n")
			for i := range count {
				fmt.Fprintf(&source, "function source%d(): void {}\n", i)
			}
			source.WriteString("function returned(index: number): Fn { switch (index) {\n")
			for i := 0; i < count-1; i++ {
				fmt.Fprintf(&source, "case %d: return source%d;\n", i, i)
			}
			fmt.Fprintf(&source, "default: return source%d;\n} }\nconst probe = returned(0); probe;\n", count-1)
			l, probe := valueFlowProbe(t, source.String())
			steps := maxValueFlowSteps

			sources := l.valueSources(probe, &steps, 0)
			assert.Assert(t, steps > 0, "these shallow bodies fit the traversal budget; only the source-count limit distinguishes them")

			if count == maxFunctionSources {
				assert.Assert(t, sources != nil, "exactly the source limit remains a proven union")
				assert.Equal(t, len(sources), count)
			} else {
				assert.Assert(t, sources == nil, "one source past the limit becomes unknown, never an incomplete or empty union")
				steps = maxValueFlowSteps
				assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
				assert.Equal(t, steps, maxValueFlowSteps, "cached exhaustion must not restart the return-body traversal")
			}
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestAdditionalUncertainReturnedSourcesStayUnknown(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{"unreachable_unknown", "function returned(): Fn { if (false) return held[0]; return first; } const probe = returned(); probe;"},
		{"async_arrow", "const returned = async (): Promise<Fn> => first; const probe = returned(); probe;"},
		{"async_expression", "const returned = async function(): Promise<Fn> { return first; }; const probe = returned(); probe;"},
		{"generator_expression", "const returned = function*(): Generator<undefined, Fn, unknown> { return first; }; const probe = returned(); probe;"},
		{"async_generator", "async function* returned(): AsyncGenerator<undefined, Fn, unknown> { return first; } const probe = returned(); probe;"},
		{"unknown_callee_in_merge", "function known(): Fn { return first; } const factories: (() => Fn)[] = [known]; const selected = true ? known : factories[0]; const probe = selected(); probe;"},
		{"unknown_return_in_merged_callee", "function known(): Fn { return first; } function other(): Fn { return held[0]; } const selected = true ? known : other; const probe = selected(); probe;"},
		{"factory_binding_write", "function returned(): Fn { return first; } function other(): Fn { return second; }\n// @ts-ignore: deliberate defensive source-only binding write\nreturned = other;\nconst probe = returned(); probe;"},
		{"parameter_cycle_through_return", "function left(f: Fn): Fn { return f; } function right(f: Fn): Fn { return left(f); } const probe = left(right(first)); left(right(second)); probe;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, returnedSourcesPrelude+tt.body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}
