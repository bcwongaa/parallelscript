package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestArrayPatternContainerSourcesResolveSelectedChildren(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"named", "const root = [{ value: first }]; const [child] = root; const probe = child.value; probe;", []string{"first"}},
				{"root_alias", "const root = [{ value: first }]; const alias = root; const [extracted] = ((alias)); const child = extracted; const probe = child.value; probe;", []string{"first"}},
				{"named_projection", "const root = { rows: [{ value: first }] }; const [child] = root.rows; const probe = child.value; probe;", []string{"first"}},
				{"fresh_projection", "const [child] = ({ rows: [{ value: first }] }).rows; const probe = child.value; probe;", []string{"first"}},
				{"extracted_root", "const root = { rows: [{ value: first }] }; const rows = root.rows; const [child] = rows; const probe = child.value; probe;", []string{"first"}},
				{"multiple_leaves", "const root = [{ value: first }, { value: second }]; const [left, right] = root; const ignored = left.value; const probe = right.value; probe;", []string{"second"}},
				{"unbound_suffix", "const root = [{ value: first }, { value: second }]; const [child] = root; const probe = child.value; probe;", []string{"first"}},
				{"array_child", "const root = [[first, second]]; const [child] = root; const probe = child[1]; probe;", []string{"second"}},
				{"nested_array_pattern", "const root = [[{ value: first }]]; const [[child]] = root; const probe = child.value; probe;", []string{"first"}},
				{"nested_object_pattern", "const root = [{ branch: { value: first } }]; const [{ branch: child }] = root; const probe = child.value; probe;", []string{"first"}},
				{"object_then_array_pattern", "const root = { rows: [{ value: first }] }; const { rows: [child] } = root; const probe = child.value; probe;", []string{"first"}},
				{"repeated_extraction", "const root = [{ value: first }]; const [child] = root; const [other] = root; const ignored = other.value; const probe = child.value; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { const root = [{ value: first }]; const [child] = root; return child.value; } const probe = selected(); probe;", []string{"first"}},
				{"closed_terminal_parameter", "function selected(value: Value): void { const root = [{ value }]; const [child] = root; const probe = child.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"terminal_union", "const root = [{ value: choose() ? first : second }]; const [child] = root; const probe = child.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps
					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
					assert.Assert(t, sources != nil, "a closed dense array binding selects its literal-valued child")
					assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames(tt.want, receivers))
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				})
			}
		})
	}
}

func TestArrayPatternContainerUnsafeUsesPoisonEverySibling(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		use  string
	}{
		{"unmodified", ""},
		{"selected_write", "other.value = second;"},
		{"sibling_write", "sibling.other = second;"},
		{"unselected_root_write", "root.unselected.value = second;"},
		{"root_array_replacement", "root.rows[0] = root.rows[1];"},
		{"captured_write", "function change(): void { other.value = second; }"},
		{"array_assignment_target", "[other.value] = [second];"},
		{"wrapped_assignment_target", "(other.value as Value) = second;"},
		{"passed_sibling", "consume(sibling);"},
		{"stored_alias", "const stored = [other];"},
		{"mutable_alias", "let mutable = other;"},
		{"dynamic_alias", "const dynamic: any = other;"},
		{"asserted_alias", "const asserted = other as { value: Value; other: Value };"},
		{"dynamic_index", "let index = 0; const ignored = root.rows[index].value;"},
		{"root_length_read", "const ignored = root.rows.length;"},
		{"omitted_binding", "const [, rebound] = root.rows;"},
		{"defaulted_binding", "const [rebound = { value: first, other: first }] = root.rows;"},
		{"rest_binding", "const [rebound, ...rest] = root.rows;"},
		{"terminal_sibling_binding", "const [rebound, { value }] = root.rows;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, receivers := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					l, _ := valueFlowProbe(t, arrayPatternContainerUseSource(receivers, tt.use))
					projections := containerAliasProbeExpressions(l)
					assert.Equal(t, len(projections), 5)
					if reverse {
						slices.Reverse(projections)
					}
					for _, projection := range projections {
						steps := maxValueFlowSteps
						sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
						if tt.use == "" || tt.name == "omitted_binding" {
							assert.Assert(t, sources != nil, "the unmodified fixture and its present-index elision prove every selected subtree")
							assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"first"}, receivers))
						} else {
							assert.Assert(t, sources == nil, "a late unsafe pattern/alias/sibling use poisons the complete root in either query order")
						}
					}
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				}
			}
		})
	}
}

func TestArrayPatternContainerUnsupportedOriginsHaveReachableControls(t *testing.T) {
	t.Parallel()
	const plain = "const root = [{ value: first }, { value: second }]; const [child, sibling] = root; const ignored = sibling.value; const probe = child.value; probe;"
	for _, tt := range []struct {
		name string
		body string
	}{
		{"mutable_pattern", strings.Replace(plain, "const [child, sibling]", "let [child, sibling]", 1)},
		{"exported_pattern", strings.Replace(plain, "const [child, sibling]", "export const [child, sibling]", 1)},
		{"mutable_root", strings.Replace(plain, "const root", "let root", 1)},
		{"any_root", strings.Replace(plain, "const root =", "const root: any =", 1)},
		{"annotated_owner", strings.Replace(plain, "[child, sibling] =", "[child, sibling]: { value: Value }[] =", 1)},
		{"asserted_origin", strings.Replace(plain, "= root;", "= (root as { value: Value }[]);", 1)},
		{"satisfies_origin", strings.Replace(plain, "= root;", "= (root satisfies { value: Value }[]);", 1)},
		{"nonnull_origin", strings.Replace(plain, "= root;", "= root!;", 1)},
		{"omission", strings.Replace(plain, "[child, sibling] =", "[child, , sibling] =", 1)},
		{"default", strings.Replace(plain, "[child, sibling] =", "[child = { value: second }, sibling] =", 1)},
		{"rest", strings.Replace(plain, "[child, sibling] = root; const ignored = sibling.value;", "[child, ...siblings] = root; const ignored = siblings[0].value;", 1)},
		{"terminal_leaf", strings.Replace(plain, "[child, sibling] = root; const ignored = sibling.value;", "[child, { value }] = root; const ignored = value;", 1)},
		{"past_end_leaf", strings.Replace(plain, "[child, sibling] =", "[child, sibling, missing] =", 1)},
		{"fresh_tuple_layout", "const [child] = [{ value: first }]; const probe = child.value; probe;"},
		{"nonliteral_child", "const inner = { value: first }; const root = [inner]; const [child] = root; const probe = child.value; probe;"},
		{"returned_container", "function selected(): { value: Value }[] { return [{ value: first }]; } const [child] = selected(); const probe = child.value; probe;"},
		{"parameter_container", "function selected(root: { value: Value }[]): void { const [child] = root; const probe = child.value; probe; } selected([{ value: first }]);"},
		{"generic_context", "function selected<T>(value: T): void { " + plain + " } selected(1);"},
		{"contextual_generic", "const selected: <T>(value: T) => void = value => { " + plain + " }; selected(1);"},
		{"holey_literal", `const root = [{ value: first }, { value: second }, ,]; const [child, sibling] = root;
// @ts-expect-error: a later hole widens the raw contract despite the selected dense prefix
const ignored = sibling.value;
// @ts-expect-error: a later hole widens the raw contract despite the selected dense prefix
const probe = child.value; probe;`},
		{"spread_literal", strings.Replace(plain, "[{ value: first }, { value: second }]", "[{ value: first }, ...[{ value: second }]]", 1)},
		{"heterogeneous_contract", strings.Replace(plain, "{ value: second }", "{ value: second, tag: 1 }", 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, control := range []bool{true, false} {
				t.Run(fmt.Sprintf("control_%t", control), func(t *testing.T) {
					for _, receivers := range []bool{false, true} {
						body := tt.body
						if control {
							body = plain
						}
						l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+body)
						steps := maxValueFlowSteps
						sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
						assert.Equal(t, sources != nil, control, "every excluded family has a reachable dense-array positive control")
						if control {
							assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"first"}, receivers))
						}
					}
				})
			}
		})
	}
}

func TestArrayPatternContainerRawAliasContractsRemainAuthoritative(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		contract string
		known    bool
	}{
		{"{ value: Value }", true},
		{"Readonly<{ value: Value }>", true},
		{"{ value: any }", false},
		{"{ value: unknown }", false},
		{"{ value?: Value }", false},
		{"{ value(): void }", false},
		{"{ get value(): Value }", false},
		{"{ value: Value } | { value: Value; tag: number }", false},
	} {
		t.Run(tt.contract, func(t *testing.T) {
			t.Parallel()
			for _, receivers := range []bool{false, true} {
				// Method/accessor contracts use callable Value, not the receiver class.
				if receivers && (strings.Contains(tt.contract, "value()") || strings.Contains(tt.contract, "get value")) {
					continue
				}
				body := "const root = [{ value: first }]; const [child] = root; const changed: " + tt.contract + " = child; const probe = root[0].value; probe;"
				l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+body)
				steps := maxValueFlowSteps
				sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
				assert.Equal(t, sources != nil, tt.known, "raw declared child contracts poison the complete array component even if a read narrows")
			}
		})
	}
}

func TestArrayPatternContainerCachesKeepKindsAndIndependentRootsSeparate(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		for _, receiverFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse_%t_receiver_first_%t", reverse, receiverFirst), func(t *testing.T) {
				t.Parallel()
				l, _ := valueFlowProbe(t, nestedContainerPrelude(true)+`const poisonedRoot = [{ value: first }]; const safeRoot = [{ value: second }];
const [poisoned] = poisonedRoot; const [safe] = safeRoot;
const check_poisoned = poisoned.value; const check_safe = safe.value; consume(poisoned); const probe = check_safe; probe;`)
				projections := containerAliasProbeExpressions(l)
				assert.Equal(t, len(projections), 2)
				if reverse {
					slices.Reverse(projections)
				}
				for range 2 {
					for _, projection := range projections {
						kinds := []bool{false, true}
						if receiverFirst {
							slices.Reverse(kinds)
						}
						for _, receivers := range kinds {
							steps := maxValueFlowSteps
							sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
							known := receivers && projection.Expression().Text() == "safe"
							assert.Equal(t, sources != nil, known)
							if known {
								assert.DeepEqual(t, returnedSourceNames(sources), []string{"B"})
							}
						}
					}
				}
			})
		}
	}
}

func TestArrayPatternContainerBoundsNeverPublishPartialFacts(t *testing.T) {
	t.Parallel()
	var deep strings.Builder
	deep.WriteString("const root = [{ value: first }]; const [alias0] = root;\n")
	for i := 1; i <= maxValueFlowDepth; i++ {
		fmt.Fprintf(&deep, "const alias%d = alias%d;\n", i, i-1)
	}
	fmt.Fprintf(&deep, "const probe = alias%d.value; probe;", maxValueFlowDepth)
	for _, tt := range []struct {
		name  string
		body  string
		steps int
	}{
		{"small_budget", "const root = [{ value: first }]; const [child] = root; const probe = child.value; probe;", 3},
		{"alias_depth", deep.String(), maxValueFlowSteps},
		{"unchecked_suffix_work", "const root = { rows: [{ value: first }], wide: [" + strings.Repeat("first,", maxValueFlowSteps+1) + "] }; const [child] = root.rows; const probe = child.value; probe;", maxValueFlowSteps},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+tt.body)
			steps := tt.steps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			if tt.name == "alias_depth" {
				assert.Assert(t, steps > 0, "depth, not work, stopped this proof")
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, steps, maxValueFlowSteps, "cached exhaustion cannot certify a partial component")
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestArrayPatternContainerShallowFanoutIsNotRecursion(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	body.WriteString("const root = [{ value: first }];\n")
	for i := range maxValueFlowDepth + 12 {
		fmt.Fprintf(&body, "const [alias%d] = root; const ignored%d = alias%d.value;\n", i, i, i)
	}
	body.WriteString("const probe = root[0].value; probe;")
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body.String())
	steps := maxValueFlowSteps
	sources := l.valueSources(probe, &steps, 0)
	assert.Assert(t, sources != nil, "independent array binding leaves consume work, not accumulated recursion depth")
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	assert.Assert(t, steps > 0)
}

func arrayPatternContainerUseSource(receivers bool, lateUse string) string {
	return nestedContainerPrelude(receivers) + `const root = { rows: [{ value: first, other: first }, { value: first, other: first }], unselected: { value: first } };
const rootAlias = root; const [extracted, sibling] = rootAlias.rows; const other = extracted;
const check_root = root.rows[0].value; const check_extracted = extracted.value; const check_alias = other.value;
const check_sibling = sibling.value; const check_unselected = root.unselected.value; const probe = check_root; probe;
` + lateUse
}
