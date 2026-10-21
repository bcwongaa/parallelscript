package lower

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
	"gotest.tools/v3/assert"
)

func TestWrapperSourcesExcludeSharedGenericContexts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		body  string
		known bool
	}{
		{"top_level", "const probe = new Maker().make; probe;", true},
		{"plain_function", "function outer(): void { const probe = new Maker().make; probe; } outer();", true},
		{"generic_function", "function outer<U>(x: U): void { const probe = new Maker().make; probe; } outer(1);", false},
		{"contextual_generic_arrow", "const outer: <U>(x: U) => void = x => { const probe = new Maker().make; probe; }; outer(1);", false},
		{"contextual_generic_expression", "const outer: <U>(x: U) => void = function(x) { const probe = new Maker().make; probe; }; outer(1);", false},
		{"generic_class", "class Outer<U> { read(): void { const probe = new Maker().make; probe; } } new Outer<number>().read();", false},
		{"nested_in_generic", "function outer<U>(x: U): void { function inner(): void { const probe = new Maker().make; probe; } inner(); } outer(1);", false},
		{"deep_context", strings.Repeat("{", maxValueFlowDepth) + "const probe = new Maker().make; probe;" + strings.Repeat("}", maxValueFlowDepth), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class Maker { make<T>(x: T): T { return x; } }\n"+tt.body)
			site := l.target(probe).ValueDeclaration.Initializer()
			assert.Check(t, (l.wrapperSource(site) != nil) == tt.known)
		})
	}
}

func TestBindWrapperSourceIsTheWholeCreationCall(t *testing.T) {
	t.Parallel()
	for _, parens := range []int{0, 2, maxValueFlowDepth + 1} {
		l, probe := valueFlowProbe(t, "function source<T>(x: T): T { return x; } const probe = "+strings.Repeat("(", parens)+"source.bind"+strings.Repeat(")", parens)+"(undefined); probe;")
		call := l.target(probe).ValueDeclaration.Initializer()
		source := l.wrapperSource(ast.SkipParentheses(call.Expression()))
		if parens <= maxValueFlowDepth {
			assert.Equal(t, source, call, "the .bind property is not the created value")
		} else {
			assert.Assert(t, source == nil, "an exhausted ancestor walk must not claim a partial creation origin")
		}
	}
}

func TestWrapperSourceRegistryKeepsUnknownAndBudgetExhaustionConservative(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		steps     int
		depth     int
		prime     bool
		wantKnown bool
	}{
		{"registered", 1, 0, false, true},
		{"no_steps", 0, 0, false, false},
		{"depth_limit", 1, maxValueFlowDepth, false, false},
		{"late_registration", 1, 0, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class Maker { make<T>(x: T): T { return x; } } const probe = new Maker().make; probe;")
			site := l.target(probe).ValueDeclaration.Initializer()
			if tt.prime {
				steps := maxValueFlowSteps
				assert.Assert(t, l.valueSources(site, &steps, 0) == nil, "a property read alone does not prove a wrapper")
			}
			l.specialization = AllInstances
			l.addForwarder(&ir.Func{}, &forwarder{source: l.wrapperSource(site)})
			steps := tt.steps
			sources := l.valueSources(site, &steps, tt.depth)
			assert.Check(t, (sources != nil) == tt.wantKnown)
			if sources != nil {
				assert.Equal(t, len(sources), 1)
				assert.Check(t, sources[site])
			}
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}
