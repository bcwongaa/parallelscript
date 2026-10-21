package lower

import (
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestReceiverSourcesProveConstructionRatherThanStaticTypes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		source string
		want   []string
	}{
		{"construction", "const probe = new A(); probe;", []string{"A"}},
		{"alias", "const a = new A(); const probe = a; probe;", []string{"A"}},
		{"union", "const probe = choose() ? new A() : new B(); probe;", []string{"A", "B"}},
		{"returned", "function make(): A { return new A(); } const probe = make(); probe;", []string{"A"}},
		{"closed_parameter", "function use(probe: A): void { probe; } use(new A()); use(new B());", []string{"A", "B"}},
		{"literal_is_not_construction", "const probe: A = {}; probe;", nil},
		{"constructor_alias", "const Constructor = A; const probe = new Constructor(); probe;", nil},
		{"written_class", "// @ts-ignore: deliberate defensive source-only class binding write\nA = B; const probe = new A(); probe;", nil},
		{"destructured_class_write", "// @ts-ignore: deliberate defensive source-only class binding write\n[A] = [B]; const probe = new A(); probe;", nil},
		{"asserted_class_write", "(A as any) = B; const probe = new A(); probe;", nil},
		{"type_asserted_class_write", "(<any>A) = B; const probe = new A(); probe;", nil},
		{"destructured_asserted_class_write", "[(A as any)] = [B]; const probe = new A(); probe;", nil},
		{"captured_class_write", "function change(): void {\n// @ts-ignore: deliberate defensive source-only class binding write\nA = B; } change(); const probe = new A(); probe;", nil},
		{"mutable_value", "let a = new A(); const probe = a; probe;", nil},
		{"dynamic_value", "const a: any = new A(); const probe: A = a; probe;", nil},
		{"narrowed_dynamic_value", "const a: any = new A(); if (a instanceof A) { const probe = a; probe; }", nil},
		{"assertion", "const probe = new A() as A; probe;", nil},
		{"generic_class", "class Generic<T> {} const probe = new Generic<number>(); probe;", nil},
		{"generic_ancestor", "class Generic<T> {} class Child extends Generic<number> {} const probe = new Child(); probe;", nil},
		{"generic_context", "function outer<T>(value: T): void { const probe = new A(); probe; } outer(1);", nil},
		{"constructor_replacement", "class Replace { constructor() { return new B(); } } const probe = new Replace(); probe;", nil},
		{"inherited_replacement", "class Replace { constructor() { return new B(); } } class Child extends Replace {} const probe = new Child(); probe;", nil},
		{"return_this_is_uncertain", "class Self { constructor() { return this; } } const probe = new Self(); probe;", nil},
		{"nested_return_is_not_replacement", "class Nested { constructor() { const f = () => new B(); f(); } } const probe = new Nested(); probe;", []string{"Nested"}},
		{"bare_constructor_return", "class Bare { constructor() { return; } } const probe = new Bare(); probe;", []string{"Bare"}},
		{"inherited_constructor", "class Child extends A {} const probe = new Child(); probe;", []string{"Child"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} class B {} function choose(): boolean { return true; }\n"+tt.source)
			steps := maxValueFlowSteps

			sources := l.receiverSources(probe, &steps, 0)

			if tt.want == nil {
				assert.Assert(t, sources == nil, "an unproven receiver must be unknown, never an empty set")
			} else {
				assert.Assert(t, sources != nil)
				assert.DeepEqual(t, returnedSourceNames(sources), tt.want)
			}
			assert.Equal(t, len(l.valueFlow.receivers.active), 0)
		})
	}
}

func TestReceiverAndFunctionReturnCachesRemainSeparate(t *testing.T) {
	t.Parallel()
	for _, receiverFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(receiverFirst), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} function make(): A { return new A(); } const probe = make(); probe;")
			functions := func() {
				steps := maxValueFlowSteps
				assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			}
			receivers := func() {
				steps := maxValueFlowSteps
				assert.DeepEqual(t, returnedSourceNames(l.receiverSources(probe, &steps, 0)), []string{"A"})
			}
			if receiverFirst {
				receivers()
				functions()
			} else {
				functions()
				receivers()
			}
			functions()
			receivers()
			assert.Equal(t, len(l.valueFlow.active), 0)
			assert.Equal(t, len(l.valueFlow.receivers.active), 0)
		})
	}
}

func TestReceiverSourceBudgetsKeepUnknown(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{0, maxValueFlowDepth} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} const probe = new A(); probe;")
			steps := 0
			if depth != 0 {
				steps = maxValueFlowSteps
			}
			assert.Assert(t, l.receiverSources(probe, &steps, depth) == nil)
			steps = maxValueFlowSteps
			assert.DeepEqual(t, returnedSourceNames(l.receiverSources(probe, &steps, 0)), []string{"A"})
		})
	}

	l, probe := valueFlowProbe(t, "class A { constructor() { let n = 0; "+strings.Repeat("n++;", 200)+" } } const probe = new A(); probe;")
	steps := 50
	assert.Assert(t, l.receiverSources(probe, &steps, 0) == nil)
	assert.Equal(t, steps, 0)
	steps = maxValueFlowSteps
	assert.Assert(t, l.receiverSources(probe, &steps, 0) == nil, "an exhausted construction summary stays conservatively unknown")
	assert.Equal(t, steps, maxValueFlowSteps)
	assert.Equal(t, len(l.valueFlow.receivers.active), 0)
}

func TestReceiverSourceCyclesRemainUnknown(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, `class A {}
function left(a: A): A { return right(a); }
function right(a: A): A { return left(a); }
const probe = left(new A()); probe;`)
	steps := maxValueFlowSteps
	assert.Assert(t, l.receiverSources(probe, &steps, 0) == nil)
	assert.Equal(t, len(l.valueFlow.receivers.active), 0)
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func TestReceiverSourceUnionLimit(t *testing.T) {
	t.Parallel()
	for _, count := range []int{maxFunctionSources, maxFunctionSources + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			for i := range count {
				fmt.Fprintf(&source, "class M%d {}\n", i)
			}
			source.WriteString("function make(index: number): object { switch (index) {\n")
			for i := 0; i < count-1; i++ {
				fmt.Fprintf(&source, "case %d: return new M%d();\n", i, i)
			}
			fmt.Fprintf(&source, "default: return new M%d(); } } const probe = make(0); probe;", count-1)
			l, probe := valueFlowProbe(t, source.String())
			steps := maxValueFlowSteps * 4 // isolate the source-count bound from constructor scans

			sources := l.receiverSources(probe, &steps, 0)

			assert.Assert(t, steps > 0)
			if count == maxFunctionSources {
				assert.Assert(t, sources != nil)
				assert.Equal(t, len(sources), count)
			} else {
				assert.Assert(t, sources == nil)
			}
			assert.Equal(t, len(l.valueFlow.receivers.active), 0)
		})
	}
}
