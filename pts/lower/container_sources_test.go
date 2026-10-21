package lower

import (
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLiteralProjectionCompleteUseAndKeyGuards(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"const held = { value: first }; delete (held.value as any); const probe = held.value; probe;",
		"const held = { value: first }; (held.value as Value) = second; const probe = held.value; probe;",
		"const held = { value: first }; [(held.value as Value)] = [second]; const probe = held.value; probe;",
		"const held = { value: first }; for (held.value of [second]) {} const probe = held.value; probe;",
		"const held = { value: first }; type Query = typeof held; const probe = held.value; probe;",
		"function replace(this: { value: Value }): void { this.value = second; } const held = { value: first, replace }; (held.replace as typeof replace)(); const probe = held.value; probe;",
		"function replace(this: { value: Value }): void { this.value = second; } const held = { value: first, replace }; held.replace!(); const probe = held.value; probe;",
		"function replace(this: { value: Value }): void { this.value = second; } const held = { value: first, replace }; (held.replace satisfies typeof replace)(); const probe = held.value; probe;",
		"const held = { value: first, set other(v: Value) {} }; const probe = held.value; probe;",
		"const extra = { value: second }; const held = { value: first, ...extra }; const probe = held.value; probe;",
		"const held = { value: first, [\"other\"]: second }; const probe = held.value; probe;",
		"const held = [first, , second]; const probe = held[0]; probe;",
		"const held = [first, second]; const other = held.length; const probe = held[0]; probe;",
		"const held = [first]; const probe = held[-0]; probe;",
		"const held = [first]; const probe = held[\"00\"]; probe;",
		"const held: { value?: Value } = { value: first }; delete held.value; const probe = held.value; probe;",
		"const dynamic: any = first; const held = { value: dynamic }; const probe = held.value; probe;",
		"const held = { value: first }; const probe = held[\"absent\"]; probe;",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			// These defensive proof guards include property-key/this syntax not all
			// supported by native lowering; deliberately suppress its tsc errors.
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+"// @ts-ignore: defensive source-only control\n"+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
		})
	}
}

func TestLiteralProjectionCanonicalDataKeys(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{
		"({ 0: first })[0]", "({ \"0\": first })[0]", "({ 0: first })[\"0\"]",
		"([first])[\"0\"]", "({ \"\": first })[\"\"]", "({ \"a-b\": first })[\"a-b\"]",
	} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+"const probe = "+expression+"; probe;")
			steps := maxValueFlowSteps
			assert.DeepEqual(t, returnedSourceNames(l.valueSources(probe, &steps, 0)), []string{"first"})
		})
	}
}

func TestLiteralProjectionExhaustedProofsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"const held = { value: first }; const probe = held.value; probe;",
		"const probe = ({ value: first, other: second }).value; probe;",
		"const held = [first, second]; const probe = held[0]; probe;",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+body)
			steps := 3
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "cached exhaustion is unknown, not empty")
			assert.Equal(t, steps, maxValueFlowSteps)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
	for _, body := range []string{
		"const held = { value: first }; " + strings.Repeat("held.value;", maxValueFlowSteps+1) + "const probe = held.value; probe;",
		"const held = [" + strings.Repeat("first,", maxValueFlowSteps+1) + "]; const probe = held[0]; probe;",
		"const held = { value: first }; " + strings.Repeat("(", maxValueFlowDepth) + "held" + strings.Repeat(")", maxValueFlowDepth) + ".value; const probe = held.value; probe;",
	} {
		t.Run(fmt.Sprintf("wide_%d", len(body)), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestLiteralProjectionCyclesRemainUnknown(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, `type Value = () => void;
function choose(): Value { return held.value; }
const held = { value: choose() }; const probe = held.value; probe;
`)
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Assert(t, steps > 0)
	assert.Equal(t, len(l.valueFlow.active), 0)
}
