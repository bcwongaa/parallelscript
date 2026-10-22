package lower

import (
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNestedContainerReadonlyCommonElementContractsStayUnknown(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+`const readonly: readonly Value[] = [first];
const root = { branch: [[first], readonly] }; const probe = root.branch[0][0]; probe;`)
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "readonly and mutable array targets are not one inferred layout proof")
}

func TestNestedContainerNonidenticalCallableElementContractsStayUnknown(t *testing.T) {
	t.Parallel()
	// These original reproducer inputs infer nonidentical callable element types.
	// Positive array cases give terminals one declared Value contract; proving a
	// conversion between distinct callable contracts is deliberately deferred.
	for _, body := range []string{
		"const probe = [[first, second]][0][1]; probe;",
		"const root = { branch: [first, second] }; const probe = root.branch[1]; probe;",
		"const root = [{ value: first }, { value: second }]; const probe = root[0].value; probe;",
		"const root = [[first, second], [second, first]]; const probe = root[1][1]; probe;",
		"const root = { branch: [{ row: [first, second] }, { row: [second, first] }] }; const probe = root.branch[1].row[0]; probe;",
		"const root = ({ branch: ({ row: [first, second] }) }); const alias = ((root)); const probe = (((alias).branch).row)[0]; probe;",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
		})
	}
}

func TestNestedContainerWideTreesUseWorkNotDepth(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	source.WriteString(literalProjectionPrelude(false))
	source.WriteString("const root = { plain: first,")
	for i := range maxValueFlowDepth + 12 {
		fmt.Fprintf(&source, "branch%d: (({ value: first })),", i)
	}
	source.WriteString("}; const alias = root; const probe = alias.plain; probe;")
	l, probe := valueFlowProbe(t, source.String())
	steps := maxValueFlowSteps

	assert.DeepEqual(t, returnedSourceNames(l.valueSources(probe, &steps, 0)), []string{"first"})
	assert.Assert(t, steps > 0, "shallow sibling literals consume work, not cumulative recursion depth")
}

func TestNestedContainerInferredContracts(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		for _, body := range []string{
			"const held = { inner: { value: first } }; const probe = held.inner.value; probe;",
			"const held = { inner: [{ value: first }] }; const alias = held; const probe = alias.inner[0].value; probe;",
		} {
			t.Run(fmt.Sprintf("receivers_%t/%s", receivers, body), func(t *testing.T) {
				t.Parallel()
				l, probe := valueFlowProbe(t, literalProjectionPrelude(receivers)+body)
				steps := maxValueFlowSteps
				sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
				assert.Assert(t, sources != nil, "a complete inferred nested tree preserves the terminal source")
				want := "first"
				if receivers {
					want = "A"
				}
				assert.DeepEqual(t, returnedSourceNames(sources), []string{want})
			})
		}
	}
}
