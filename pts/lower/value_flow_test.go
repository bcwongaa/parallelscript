package lower

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"gotest.tools/v3/assert"
)

func TestFunctionSourcesDistinguishUnknownFromNoFunction(t *testing.T) {
	t.Parallel()
	node := &ast.Node{Kind: ast.KindArrowFunction}
	known := functionSources{node: true}
	assert.Assert(t, mergeFunctionSources(known, nil) == nil)
	assert.Assert(t, mergeFunctionSources(nil, known) == nil)
	assert.DeepEqual(t, mergeFunctionSources(functionSources{}, known), known)
	assert.DeepEqual(t, mergeFunctionSources(known, known), known)
	assert.DeepEqual(t, mergeFunctionSources(functionSources{}, functionSources{}), functionSources{})
}

func TestFunctionSourcesLargeUnionsAreUnknown(t *testing.T) {
	t.Parallel()
	sources := functionSources{}
	for range maxFunctionSources {
		sources[&ast.Node{Kind: ast.KindArrowFunction}] = true
	}
	assert.Equal(t, len(mergeFunctionSources(sources, sources)), maxFunctionSources)
	assert.Assert(t, mergeFunctionSources(sources, functionSources{&ast.Node{Kind: ast.KindArrowFunction}: true}) == nil)
}

func TestValueFlowDepthAndStepBudgetsKeepUnknown(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	source.WriteString("const source = (): void => {};\nconst alias0 = source;\n")
	for i := 1; i <= maxValueFlowDepth; i++ {
		fmt.Fprintf(&source, "const alias%d = alias%d;\n", i, i-1)
	}
	fmt.Fprintf(&source, "const probe = alias%d;\nprobe;\n", maxValueFlowDepth)
	l, probe := valueFlowProbe(t, source.String())
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Assert(t, steps > 0, "depth, not the step budget, stopped this chain")
	steps = maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Equal(t, steps, maxValueFlowSteps, "a cached unknown must not restart analysis")

	l, probe = valueFlowProbe(t, "const source = (): void => {}; const probe = source; probe;")
	steps = 1
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Equal(t, steps, 0)
}

func TestWrittenFunctionDeclarationsHaveUnknownSources(t *testing.T) {
	t.Parallel()
	for _, write := range []string{
		"first = second;",
		"[first] = [second];",
		"({ make: first } = { make: second });",
		"function replace(): void { first = second; } replace();",
		"for (first of [second]) {}",
	} {
		t.Run(write, func(t *testing.T) {
			t.Parallel()
			// tsc normally rejects writes to function declarations (TS2630). Suppress it
			// explicitly to test this defensive source analysis, not executable lowering.
			l, probe := valueFlowProbe(t, "function first(): void {} function second(): void {}\n// @ts-ignore: deliberate function-binding write\n"+write+"\nconst probe = first; probe;")
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
		})
	}
}

func TestValueFlowRecursiveCallerIsUnknown(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, `function recur(probe: () => void, n: number): void {
  probe;
  if (n > 0) recur(probe, n - 1);
}
recur(() => {}, 2);
`)
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Assert(t, steps > 0)
	assert.Equal(t, len(l.valueFlow.active), 0, "cycles leave no in-progress proofs")
}

func TestShorthandDefaultEscapesTheConsumer(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, `const first = (): void => {};
const second = (): void => {};
function consume(probe: () => void): void { probe; }
consume(first);
let alias: (f: () => void) => void = (_f: () => void): void => {};
const options: { alias?: (f: () => void) => void } = {};
({ alias = consume } = options);
alias(second);
`)
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "the default initializer reads consume, not alias")
}

func TestParameterSourceJoinsChargeCachedArguments(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, `const source = (): void => {};
function consume(probe: () => void): void { probe; }
consume(source); consume(source); consume(source); consume(source);
`)
	parameter := l.target(probe).ValueDeclaration
	for _, call := range l.functionValueUses(l.target(parameter.Parent.Name())).calls {
		steps := maxValueFlowSteps
		assert.Assert(t, l.valueSources(call.Arguments.Nodes[0], &steps, 0) != nil)
	}
	steps := 3
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "caller joins consume work even when argument proofs are cached")
	assert.Equal(t, steps, 0)
}

func TestParameterSourceProofIsSharedAcrossReads(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, `const source = (): void => {};
function consume(probe: () => void): void { probe; probe; }
consume(source); consume(source); consume(source); consume(source);
`)
	parameter := l.target(probe).ValueDeclaration
	first := parameter.Parent.Body().AsBlock().Statements.Nodes[0].Expression()
	steps := maxValueFlowSteps
	sources := l.valueSources(first, &steps, 0)
	assert.Assert(t, sources != nil)
	steps = 1
	assert.DeepEqual(t, l.valueSources(probe, &steps, 0), sources)
	assert.Equal(t, steps, 0)
}

func TestSourceBindingWriteAssertionsKeepFunctionProofsUnknown(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"(first as any)", "(<any>first)", "[(first as any)]"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			value := "second"
			if target[0] == '[' {
				value = "[second]"
			}
			l, probe := valueFlowProbe(t, "function first(): void {} function second(): void {} "+target+" = "+value+"; const probe = first; probe;")
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
		})
	}
}

// valueFlowProbe builds the source-only analysis and returns the expression statement
// `probe;`, allowing tests to exercise proof limits without manufacturing checker nodes.
func valueFlowProbe(t *testing.T, source string) (*lowerer, *ast.Node) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{"/p/main.pts": source}, true)
	program, errors := frontend.Load(t.Context(), fs, "/p/main.pts")
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	c, release := program.TS.GetTypeCheckerForFile(t.Context(), program.Entry)
	t.Cleanup(release)
	l := &lowerer{program: program.TS, checker: c, valueFlow: valueFlow{
		files: program.Modules,
	}}
	l.collectOutsideAssignments(program.Modules)
	var probe *ast.Node
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindExpressionStatement && node.Expression().Kind == ast.KindIdentifier && node.Expression().Text() == "probe" {
			probe = node.Expression()
		}
		node.ForEachChild(visit)
		return false
	}
	program.Entry.AsNode().ForEachChild(visit)
	assert.Assert(t, probe != nil)
	return l, probe
}
