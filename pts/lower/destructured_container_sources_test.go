package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestDestructuredContainerSourcesResolveSelectedChildren(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"named_renamed", "const root = { child: { value: first } }; const { child: extracted } = root; const probe = extracted.value; probe;", []string{"first"}},
				{"original_inline_pattern", "const { child: extracted } = { child: { value: first } }; const probe = extracted.value; probe;", []string{"first"}},
				{"fresh_shorthand", "const { child } = { child: { value: first } }; const probe = child.value; probe;", []string{"first"}},
				{"whole_root_alias", "const root = { child: { value: first } }; const alias = root; const { child: extracted } = ((alias)); const probe = extracted.value; probe;", []string{"first"}},
				{"named_projection", "const root = { branch: { child: { value: first } } }; const { child: extracted } = root.branch; const probe = extracted.value; probe;", []string{"first"}},
				{"fresh_projection", "const { child: extracted } = ({ branch: { child: { value: first } } }).branch; const probe = extracted.value; probe;", []string{"first"}},
				{"nested_pattern", "const { branch: { child: extracted } } = { branch: { child: { value: first } } }; const probe = extracted.value; probe;", []string{"first"}},
				{"named_nested_pattern", "const root = { branch: { child: { value: first } } }; const { branch: { child: extracted } } = root; const probe = extracted.value; probe;", []string{"first"}},
				{"multiple_nested_leaves", "const root = { branch: { child: { value: first }, side: [second] } }; const { branch: { child, side } } = root; const ignored = side[0]; const probe = child.value; probe;", []string{"first"}},
				{"quoted_key", "const { \"child\": extracted } = { child: { value: first } }; const probe = extracted[\"value\"]; probe;", []string{"first"}},
				{"array_child", "const { child: extracted } = { child: [first, second] }; const probe = extracted[1]; probe;", []string{"second"}},
				{"all_child_leaves", "const root = { child: { value: first }, side: [second] }; const { child, side } = root; const ignored = side[0]; const probe = child.value; probe;", []string{"first"}},
				{"repeated_extraction", "const root = { child: { value: first } }; const { child: extracted } = root; const { child: repeated } = root; const alias = extracted; const ignored = repeated.value; const probe = alias.value; probe;", []string{"first"}},
				{"subtree_extraction", "const { branch } = { branch: { rows: [{ value: first }, { value: second }] } }; const rows = branch.rows; const row = rows[1]; const probe = row.value; probe;", []string{"second"}},
				{"readonly_child_alias", "const { child } = { child: { value: first } }; const alias: Readonly<{ value: Value }> = child; const probe = alias.value; probe;", []string{"first"}},
				{"terminal_return", "function selected(): Value { return first; } const { child } = { child: { value: selected() } }; const probe = child.value; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { const { child } = { child: { value: first } }; return child.value; } const probe = selected(); probe;", []string{"first"}},
				{"closed_terminal_parameter", "function selected(value: Value): void { const { child } = { child: { value } }; const probe = child.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"terminal_union", "const { child } = { child: { value: choose() ? first : second } }; const probe = child.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps

					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)

					assert.Assert(t, sources != nil, "a closed object binding selects its literal-valued child, not an unknown source")
					assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames(tt.want, receivers))
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				})
			}
		})
	}
}

func TestDestructuredContainerUnsafeUsesPoisonTheCompleteRoot(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		use  string
	}{
		{"unmodified", ""},
		{"selected_write", "extracted.value = second;"},
		{"alias_write", "other.other = second;"},
		{"sibling_write", "sibling.value = second;"},
		{"unselected_root_write", "rootAlias.unselected.value = second;"},
		{"intermediate_replacement", "root.branch.child = root.branch.side;"},
		{"captured_write", "function change(): void { other.value = second; }"},
		{"pattern_target_write", "({ value: other.value } = { value: second });"},
		{"wrapped_target_write", "(other.value as Value) = second;"},
		{"passed_sibling", "consume(sibling);"},
		{"stored_alias", "const stored = [other];"},
		{"mutable_alias", "let mutable = other;"},
		{"dynamic_alias", "const dynamic: any = other;"},
		{"asserted_alias", "const asserted = other as { value: Value; other: Value };"},
		{"dynamic_key", "let key: \"value\" = \"value\"; const ignored = other[key];"},
		{"terminal_binding", "const { value } = other;"},
		{"defaulted_binding", "const { child: defaulted = { value: first, other: first } } = rootAlias.branch;"},
		{"rest_binding", "const { child: rebound, ...rest } = rootAlias.branch;"},
		{"terminal_sibling_binding", "const { child: rebound, plain } = { child: other, plain: first };"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, receivers := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					l, _ := valueFlowProbe(t, destructuredContainerUseSource(receivers, tt.use))
					projections := containerAliasProbeExpressions(l)
					assert.Equal(t, len(projections), 5)
					if reverse {
						slices.Reverse(projections)
					}
					for _, projection := range projections {
						steps := maxValueFlowSteps
						sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
						if tt.use == "" {
							assert.Assert(t, sources != nil, "the identical unmodified fixture must have a reachable positive proof")
							assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"first"}, receivers))
						} else {
							assert.Assert(t, sources == nil, "late root, sibling, alias or unsupported binding use poisons every subtree in either cache order")
						}
					}
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				}
			}
		})
	}
}

func TestDestructuredContainerUnsupportedPatternsAndOriginsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		body string
	}{
		{"mutable", "let { child } = { child: { value: first } }; const probe = child.value; probe;"},
		{"exported", "export const { child } = { child: { value: first } }; const probe = child.value; probe;"},
		{"default", "const { child = { value: second } } = { child: { value: first } }; const probe = child.value; probe;"},
		{"nested_default", "const { branch: { child = { value: second } } } = { branch: { child: { value: first } } }; const probe = child.value; probe;"},
		{"nested_rest", "const { branch: { child, ...rest } } = { branch: { child: { value: first }, side: { value: second } } }; const probe = child.value; probe;"},
		{"annotated_binding_owner", "const root = { child: { value: first } }; const { child }: { child: { value: Value } } = root; const probe = child.value; probe;"},
		{"rest", "const { child, ...rest } = { child: { value: first }, side: { value: second } }; const probe = child.value; probe;"},
		{"computed_literal_key", "const { [\"child\"]: child } = { child: { value: first } }; const probe = child.value; probe;"},
		{"computed_dynamic_key", "let key: \"child\" = \"child\"; const { [key]: child } = { child: { value: first } }; const probe = child.value; probe;"},
		{"array_pattern", "const [child] = [{ value: first }]; const probe = child.value; probe;"},
		{"numeric_pattern_key", "const { 0: child } = { 0: { value: first } }; const probe = child.value; probe;"},
		{"array_root", "const { \"0\": child } = [{ value: first }]; const probe = child.value; probe;"},
		{"assignment_pattern", "const root = { child: { value: first } }; let child = { value: second }; ({ child } = root); const probe = root.child.value; probe;"},
		{"parameter_pattern", "function selected({ child }: { child: { value: Value } }): void { const probe = child.value; probe; } selected({ child: { value: first } });"},
		{"terminal_function_sibling", "const { child, terminal } = { child: { value: first }, terminal: second }; const probe = child.value; probe;"},
		{"terminal_primitive_sibling", "const { child, terminal } = { child: { value: first }, terminal: 1 }; const probe = child.value; probe;"},
		{"nested_terminal_leaf", "const { child, side: { terminal } } = { child: { value: first }, side: { terminal: second } }; const probe = child.value; probe;"},
		{"any_origin", "const root: any = { child: { value: first } }; const { child } = root; const probe = child.value; probe;"},
		{"asserted_origin", "const { child } = ({ child: { value: first } } as { child: { value: Value } }); const probe = child.value; probe;"},
		{"satisfies_origin", "const { child } = ({ child: { value: first } } satisfies { child: { value: Value } }); const probe = child.value; probe;"},
		{"nonnull_origin", "const root = { child: { value: first } }; const { child } = root!; const probe = child.value; probe;"},
		{"nonliteral_child", "const inner = { value: first }; const { child } = { child: inner }; const probe = child.value; probe;"},
		{"returned_container", "function selected(): { child: { value: Value } } { return { child: { value: first } }; } const { child } = selected(); const probe = child.value; probe;"},
		{"generic_context", "function outer<T>(value: T): void { const { child } = { child: { value: first } }; const probe = child.value; probe; } outer(1);"},
		{"contextual_generic", "const outer: <T>(value: T) => void = value => { const { child } = { child: { value: first } }; const probe = child.value; probe; }; outer(1);"},
		{"invalid_unselected_getter", "const root = { child: { value: first }, side: { get value(): Value { return second; } } }; const { child } = root; const probe = child.value; probe;"},
		{"invalid_unselected_spread", "const root = { child: { value: first }, side: { ...{ value: second } } }; const { child } = root; const probe = child.value; probe;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+tt.body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "unsupported pattern or uncertain origin must not publish a selected child")
		})
	}
}

func TestDestructuredContainerDeclaredContractsCannotBeNarrowedAway(t *testing.T) {
	t.Parallel()
	for _, contract := range []string{
		"{ value: any }", "{ value: unknown }", "{ value?: Value }", "{ value(): void }",
		"{ get value(): Value }", "{ value: Value } | { value: Value; tag: number }",
	} {
		t.Run(contract, func(t *testing.T) {
			t.Parallel()
			body := "const root = { child: { value: first } }; const { child } = root; const changed: " + contract + " = child; const probe = root.child.value; probe;"
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "a child alias's raw declared contract poisons the original root too")
			body = "const root = { child: { value: first } }; const { child }: { child: " + contract + " } = root; const probe = root.child.value; probe;"
			l, probe = valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "the binding edge cannot erase its declared child contract")
		})
	}
	t.Run("readonly_array", func(t *testing.T) {
		t.Parallel()
		l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+"const root = { child: [first, second] }; const { child } = root; const changed: readonly Value[] = child; const probe = root.child[0]; probe;")
		steps := maxValueFlowSteps
		assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	})
}

func TestDestructuredContainerCacheKindsAndIndependentRootsStaySeparate(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		for _, receiverFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse_%t_receiver_first_%t", reverse, receiverFirst), func(t *testing.T) {
				t.Parallel()
				l, _ := valueFlowProbe(t, nestedContainerPrelude(true)+`const poisonedRoot = { child: { value: first } };
const safeRoot = { child: { value: second } };
const { child: poisoned } = poisonedRoot; const { child: safe } = safeRoot;
const check_poisoned = poisoned.value; const check_safe = safe.value;
consume(poisoned); const probe = check_safe; probe;`)
				projections := containerAliasProbeExpressions(l)
				assert.Equal(t, len(projections), 2)
				if reverse {
					slices.Reverse(projections)
				}
				for range 2 {
					for _, projection := range projections {
						proofs := []bool{false, true}
						if receiverFirst {
							slices.Reverse(proofs)
						}
						for _, receivers := range proofs {
							steps := maxValueFlowSteps
							sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
							if receivers && projection.Expression().Text() == "safe" {
								assert.Assert(t, sources != nil)
								assert.DeepEqual(t, returnedSourceNames(sources), []string{"B"})
							} else {
								assert.Assert(t, sources == nil)
							}
						}
					}
				}
				assert.Equal(t, len(l.valueFlow.active), 0)
				assert.Equal(t, len(l.valueFlow.receivers.active), 0)
			})
		}
	}
}

func TestDestructuredContainerFanoutUsesWorkNotAccumulatedDepth(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	body.WriteString("const root = { child: { value: first } };\n")
	for i := range maxValueFlowDepth + 12 {
		fmt.Fprintf(&body, "const { child: alias%d } = root; const selected%d = alias%d.value;\n", i, i, i)
	}
	body.WriteString("const probe = root.child.value; probe;")
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body.String())
	steps := maxValueFlowSteps
	sources := l.valueSources(probe, &steps, 0)
	assert.Assert(t, sources != nil, "independent shallow binding leaves are fanout, not one deep path")
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	assert.Assert(t, steps > 0)
}

func TestDestructuredContainerExhaustionCannotPublishPartialPositiveFacts(t *testing.T) {
	t.Parallel()
	var deep strings.Builder
	deep.WriteString("const root = { child: { value: first } }; const { child: alias0 } = root;\n")
	for i := 1; i <= maxValueFlowDepth; i++ {
		fmt.Fprintf(&deep, "const alias%d = alias%d;\n", i, i-1)
	}
	fmt.Fprintf(&deep, "const probe = alias%d.value; probe;", maxValueFlowDepth)
	for _, tt := range []struct {
		name  string
		body  string
		steps int
	}{
		{"small_budget", "const root = { child: { value: first } }; const { child } = root; const probe = child.value; probe;", 3},
		{"alias_depth", deep.String(), maxValueFlowSteps},
		{"unselected_root_work", "const root = { child: { value: first }, wide: [" + strings.Repeat("first,", maxValueFlowSteps+1) + "] }; const { child } = root; const probe = child.value; probe;", maxValueFlowSteps},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+tt.body)
			steps := tt.steps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			if tt.name == "alias_depth" {
				assert.Assert(t, steps > 0)
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, steps, maxValueFlowSteps, "cached exhaustion remains unknown without rechecking a partial component")
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestDestructuredContainerExhaustionDoesNotCertifyUncheckedSiblings(t *testing.T) {
	t.Parallel()
	l, _ := valueFlowProbe(t, destructuredContainerUseSource(false, "consume(sibling);"))
	projections := containerAliasProbeExpressions(l)
	assert.Equal(t, len(projections), 5)
	steps := 12
	assert.Assert(t, l.valueSources(projections[0], &steps, 0) == nil)
	for _, projection := range projections {
		steps = maxValueFlowSteps
		assert.Assert(t, l.valueSources(projection, &steps, 0) == nil, "an exhausted partial component cannot certify any unchecked alias or sibling")
	}
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func TestDestructuredContainerSourceCyclesStayUnknown(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+"function selected(): Value { return child.value; } const { child } = { child: { value: selected() } }; const probe = child.value; probe;")
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Assert(t, steps > 0)
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func destructuredContainerUseSource(receivers bool, lateUse string) string {
	return nestedContainerPrelude(receivers) + `const root = { branch: { child: { value: first, other: first }, side: { value: first, other: first } }, unselected: { value: first } };
const rootAlias = root; const { child: extracted, side: sibling } = rootAlias.branch; const other = extracted;
const check_root = root.branch.child.value; const check_extracted = extracted.value; const check_alias = other.value;
const check_sibling = sibling.value; const check_unselected = root.unselected.value;
const probe = check_root; probe;
` + lateUse
}
