package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"gotest.tools/v3/assert"
)

func TestElidedContainerSourcesSelectTheirOriginalLiteralIndices(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"leading", "const root = [{ value: first }, { value: second }]; const [, child] = root; const probe = child.value; probe;", []string{"second"}},
				{"interior", "const root = [{ value: first }, { value: first }, { value: second }]; const [left, , child] = root; const ignored = left.value; const probe = child.value; probe;", []string{"second"}},
				{"trailing", "const root = [{ value: first }, { value: second }]; const [child, ,] = root; const probe = child.value; probe;", []string{"first"}},
				{"multiple_leading", "const root = [{ value: first }, { value: first }, { value: second }]; const [, , child] = root; const probe = child.value; probe;", []string{"second"}},
				{"interior_and_trailing", "const root = [{ value: first }, { value: first }, { value: second }, { value: first }]; const [left, , child, ,] = root; const ignored = left.value; const probe = child.value; probe;", []string{"second"}},
				{"multiple_leaves", "const root = [{ value: first }, { value: first }, { value: second }, { value: first }, { value: first }]; const [left, , child, , right] = root; const ignored = left.value; const alsoIgnored = right.value; const probe = child.value; probe;", []string{"second"}},
				{"root_alias", "const root = [{ value: first }, { value: second }]; const rootAlias = root; const [, extracted] = ((rootAlias)); const child = extracted; const probe = child.value; probe;", []string{"second"}},
				{"named_projection", "const root = { rows: [{ value: first }, { value: second }] }; const [, child] = root.rows; const probe = child.value; probe;", []string{"second"}},
				{"fresh_projection", "const [, child] = ({ rows: [{ value: first }, { value: second }] }).rows; const probe = child.value; probe;", []string{"second"}},
				{"extracted_root", "const root = { rows: [{ value: first }, { value: second }] }; const rows = root.rows; const [, child] = rows; const probe = child.value; probe;", []string{"second"}},
				{"array_child", "const root = [[first, first], [first, second]]; const [, child] = root; const probe = child[1]; probe;", []string{"second"}},
				{"nested_array", "const root = [[{ value: first }, { value: first }], [{ value: first }, { value: second }]]; const [, [, child]] = root; const probe = child.value; probe;", []string{"second"}},
				{"nested_object", "const root = [{ rows: [{ value: first }, { value: second }] }]; const [{ rows: [, child] }] = root; const probe = child.value; probe;", []string{"second"}},
				{"object_array", "const root = { rows: [{ value: first }, { value: second }] }; const { rows: [, child] } = root; const probe = child.value; probe;", []string{"second"}},
				{"mixed_nested", "const root = [{ rows: [{ value: first }, { value: first }] }, { rows: [{ value: first }, { value: second }] }]; const [, { rows: [, child] }] = root; const probe = child.value; probe;", []string{"second"}},
				{"all_elided_root_use", "const root = [{ value: first }, { value: second }]; const [, ,] = root; const probe = root[1].value; probe;", []string{"second"}},
				{"all_elided_repeated_use", "const root = [{ value: first }, { value: second }]; const [, child] = root; const [, ,] = root; const probe = child.value; probe;", []string{"second"}},
				{"all_elided_nested_sibling", "const root = [{ rows: [{ value: first }, { value: first }] }, { rows: [{ value: first }, { value: second }] }]; const [{ rows: [child] }, { rows: [, ,] }] = root; const probe = child.value; probe;", []string{"first"}},
				{"all_elided_terminal_sibling", "const root = { child: { value: first }, skipped: [first, second] }; const { child, skipped: [, ,] } = root; const probe = child.value; probe;", []string{"first"}},
				{"all_elided_primitive_sibling", "const root = { child: { value: first }, skipped: [1, 2] }; const { child, skipped: [, ,] } = root; const probe = child.value; probe;", []string{"first"}},
				{"repeated_extraction", "const root = [{ value: first }, { value: second }]; const [, extracted] = root; const [, repeated] = root; const child = extracted; const ignored = repeated.value; const probe = child.value; probe;", []string{"second"}},
				{"selected_return", "function selected(): Value { const root = [{ value: first }, { value: second }]; const [, child] = root; return child.value; } const probe = selected(); probe;", []string{"second"}},
				{"closed_terminal_parameter", "function selected(value: Value): void { const root = [{ value: first }, { value }]; const [, child] = root; const probe = child.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"terminal_union", "const root = [{ value: first }, { value: choose() ? first : second }]; const [, child] = root; const probe = child.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps
					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
					assert.Assert(t, sources != nil, "elisions advance the literal index without introducing a binding or compacting later selections")
					assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames(tt.want, receivers))
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				})
			}
		})
	}
}

func TestElidedContainerUnsafeUsesPoisonSkippedAndSelectedSubtrees(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		use  string
	}{
		{"unmodified", ""},
		{"selected_write", "other.value = second;"},
		{"sibling_write", "sibling.other = second;"},
		{"elided_element_write", "root.rows[0].other = second;"},
		{"unbound_suffix_write", "root.rows[4].value = second;"},
		{"unselected_root_write", "root.unselected.value = second;"},
		{"root_array_replacement", "rootAlias.rows[2] = root.rows[1];"},
		{"captured_write", "function change(): void { other.value = second; }"},
		{"array_assignment_target", "[other.value] = [second];"},
		{"wrapped_assignment_target", "(other.value as Value) = second;"},
		{"passed_sibling", "consume(sibling);"},
		{"passed_elided_element", "consume(root.rows[0]);"},
		{"returned_sibling", "function escape() { return sibling; }"},
		{"stored_alias", "const stored = [other];"},
		{"mutable_alias", "let mutable = other;"},
		{"dynamic_alias", "const dynamic: any = other;"},
		{"asserted_alias", "const asserted = other as { value: Value; other: Value };"},
		{"dynamic_index", "let index = 0; const ignored = root.rows[index].value;"},
		{"root_length_read", "const ignored = root.rows.length;"},
		{"root_iterator_call", "const ignored = root.rows.values();"},
		{"default_after_elision", "const [, rebound = { value: first, other: first }] = root.rows;"},
		{"rest_after_elision", "const [, rebound, ...rest] = root.rows;"},
		{"terminal_sibling_after_elision", "const [, rebound, , { value }] = root.rows;"},
		{"annotated_pattern_owner", "const [, rebound]: { value: Value; other: Value }[] = root.rows;"},
		{"elision_past_end", "const [, , , , , ,] = root.rows;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, receivers := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					l, _ := valueFlowProbe(t, elidedContainerUseSource(receivers, tt.use))
					projections := containerAliasProbeExpressions(l)
					assert.Equal(t, len(projections), 7)
					if reverse {
						slices.Reverse(projections)
					}
					for range 2 {
						for _, projection := range projections {
							steps := maxValueFlowSteps
							sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
							if tt.use == "" {
								assert.Assert(t, sources != nil, "the identical elided fixture without its late unsafe use must prove every subtree")
								assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"first"}, receivers))
							} else {
								assert.Assert(t, sources == nil, "unsafe root, skipped element, sibling or transitive alias poisons the complete component in either cache order")
							}
						}
					}
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				}
			}
		})
	}
}

func TestElidedContainerUnsupportedOriginsKeepTheirDenseSafetyBoundaries(t *testing.T) {
	t.Parallel()
	const plain = "const root = [{ value: first }, { value: second }]; const [, child] = root; const probe = child.value; probe;"
	for _, tt := range []struct {
		name string
		body string
	}{
		{"unmodified", plain},
		{"mutable_pattern", strings.Replace(plain, "const [, child]", "let [, child]", 1)},
		{"exported_pattern", strings.Replace(plain, "const [, child]", "export const [, child]", 1)},
		{"mutable_root", strings.Replace(plain, "const root", "let root", 1)},
		{"any_root", strings.Replace(plain, "const root =", "const root: any =", 1)},
		{"annotated_root", strings.Replace(plain, "const root =", "const root: { value: Value }[] =", 1)},
		{"annotated_owner", strings.Replace(plain, "[, child] =", "[, child]: { value: Value }[] =", 1)},
		{"asserted_origin", strings.Replace(plain, "= root;", "= (root as { value: Value }[]);", 1)},
		{"satisfies_origin", strings.Replace(plain, "= root;", "= (root satisfies { value: Value }[]);", 1)},
		{"nonnull_origin", strings.Replace(plain, "= root;", "= root!;", 1)},
		{"default", strings.Replace(plain, "[, child] =", "[, child = { value: first }] =", 1)},
		{"rest", "const root = [{ value: first }, { value: second }]; const [, ...children] = root; const probe = children[0].value; probe;"},
		{"terminal_leaf", "const root = [{ value: first }, { value: second }, { value: first }]; const [, child, { value }] = root; const probe = child.value; probe;"},
		{"primitive_terminal_leaf", "const root = { rows: [{ value: first }, { value: second }], terminal: 1 }; const { rows: [, child], terminal } = root; const probe = child.value; probe;"},
		{"past_end_binding", strings.Replace(plain, "[, child] =", "[, child, missing] =", 1)},
		{"past_end_elision", strings.Replace(plain, "[, child] =", "[, child, ,] =", 1)},
		{"all_elided_past_end", strings.Replace(plain, "const probe", "const [, , ,] = root; const probe", 1)},
		{"fresh_tuple_layout", "const [, child] = [{ value: first }, { value: second }]; const probe = child.value; probe;"},
		{"nonliteral_child", "const inner = { value: second }; const root = [inner, inner]; const [, child] = root; const probe = child.value; probe;"},
		{"returned_container", "function selected(): { value: Value }[] { return [{ value: first }, { value: second }]; } const [, child] = selected(); const probe = child.value; probe;"},
		{"parameter_container", "function selected(root: { value: Value }[]): void { const [, child] = root; const probe = child.value; probe; } selected([{ value: first }, { value: second }]);"},
		{"generic_context", "function selected<T>(value: T): void { " + plain + " } selected(1);"},
		{"contextual_generic", "const selected: <T>(value: T) => void = value => { " + plain + " }; selected(1);"},
		{"heterogeneous_contract", strings.Replace(plain, "{ value: first }", "{ value: first, tag: 1 }", 1)},
		{"elided_getter_subtree", "const root = [{ value: first }, { value: second, get other(): Value { return first; } }]; const [child, ,] = root; const probe = child.value; probe;"},
		{"elided_spread_subtree", "const root = [{ value: first }, { ...{ value: second } }]; const [child, ,] = root; const probe = child.value; probe;"},
		{"unselected_holey_subtree", "const root = { rows: [{ value: first }, { value: second }], skipped: [first, ,] }; const [, child] = root.rows; const probe = child.value; probe;"},
		{"unselected_computed_subtree", "const root = { rows: [{ value: first }, { value: second }], skipped: { [\"value\"]: first } }; const [, child] = root.rows; const probe = child.value; probe;"},
		{"all_elided_uncertain_contract", "const root = { child: { value: first }, skipped: [1, \"two\"] }; const { child, skipped: [, ,] } = root; const probe = child.value; probe;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, receivers := range []bool{false, true} {
				l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
				steps := maxValueFlowSteps
				sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
				assert.Equal(t, sources != nil, tt.name == "unmodified", "omissions do not widen any other origin, contract, layout or binding boundary")
				if sources != nil {
					assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"second"}, receivers))
				}
			}
		})
	}
}

func TestElidedContainerRawContractsPoisonTheWholeRoot(t *testing.T) {
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
				if receivers && (strings.Contains(tt.contract, "value()") || strings.Contains(tt.contract, "get value")) {
					continue
				}
				for _, reverse := range []bool{false, true} {
					body := "const root = [{ value: first }, { value: second }]; const [, child] = root; const alias = child; const changed: " + tt.contract + " = alias; const check_root = root[0].value; const check_child = child.value; const check_alias = alias.value; const probe = check_child; probe;"
					l, _ := valueFlowProbe(t, nestedContainerPrelude(receivers)+body)
					projections := containerAliasProbeExpressions(l)
					assert.Equal(t, len(projections), 3)
					if reverse {
						slices.Reverse(projections)
					}
					for _, projection := range projections {
						steps := maxValueFlowSteps
						sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
						assert.Equal(t, sources != nil, tt.known, "raw alias contracts remain authoritative even when the queried projection is elsewhere")
						if tt.known {
							want := "second"
							if projection.Expression().Kind == ast.KindElementAccessExpression {
								want = "first"
							}
							assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{want}, receivers))
						}
					}
				}
			}
		})
	}
}

func TestElidedContainerCachesIsolateKindsRootsAndQueryOrder(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		for _, receiverFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse_%t_receiver_first_%t", reverse, receiverFirst), func(t *testing.T) {
				t.Parallel()
				l, _ := valueFlowProbe(t, nestedContainerPrelude(true)+`type Callable = () => void;
const callFirst: Callable = function callFirst(): void {};
const callSecond: Callable = function callSecond(): void {};
const poisonedRoot = [{ call: callFirst, receiver: first }, { call: callSecond, receiver: second }];
const safeRoot = [{ call: callFirst, receiver: first }, { call: callSecond, receiver: second }];
const [, poisoned] = poisonedRoot; const [, safe] = safeRoot;
const check_poisoned_call = poisoned.call; const check_poisoned_receiver = poisoned.receiver;
const check_safe_call = safe.call; const check_safe_receiver = safe.receiver;
consume(poisoned); const probe = check_safe_call; probe;`)
				projections := containerAliasProbeExpressions(l)
				assert.Equal(t, len(projections), 4)
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
							known := projection.Expression().Text() == "safe" && receivers == (projection.Name().Text() == "receiver")
							assert.Equal(t, sources != nil, known)
							if known {
								want := "callSecond"
								if receivers {
									want = "B"
								}
								assert.DeepEqual(t, returnedSourceNames(sources), []string{want})
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

func TestElidedContainerBoundsNeverPublishPartialFacts(t *testing.T) {
	t.Parallel()
	const plain = "const root = [{ value: first }, { value: second }]; const [, child] = root; const probe = child.value; probe;"
	var deep, repeated strings.Builder
	deep.WriteString("const root = [{ value: first }, { value: second }]; const [, alias0] = root;\n")
	for i := 1; i <= maxValueFlowDepth; i++ {
		fmt.Fprintf(&deep, "const alias%d = alias%d;\n", i, i-1)
	}
	fmt.Fprintf(&deep, "const probe = alias%d.value; probe;", maxValueFlowDepth)
	repeated.WriteString("const root = [{ value: first }, { value: second }]; const [, child] = root;\n")
	for range maxValueFlowSteps/2 + 1 {
		repeated.WriteString("const [, ,] = root;\n")
	}
	repeated.WriteString("const probe = child.value; probe;")
	for _, tt := range []struct {
		name  string
		body  string
		steps int
	}{
		{"small_budget", plain, 3},
		{"alias_depth", deep.String(), maxValueFlowSteps},
		{"unselected_tree_work", "const root = { rows: [{ value: first }, { value: second }], skipped: [" + strings.Repeat("first,", maxValueFlowSteps+1) + "] }; const [, child] = root.rows; const probe = child.value; probe;", maxValueFlowSteps},
		{"all_elided_uses_charge_work", repeated.String(), maxValueFlowSteps},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, receivers := range []bool{false, true} {
				l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
				steps := tt.steps
				assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0) == nil)
				if tt.name == "alias_depth" {
					assert.Assert(t, steps > 0, "depth, not work, stopped the proof")
				}
				if tt.name == "all_elided_uses_charge_work" {
					assert.Equal(t, steps, 0, "nameless pattern elements and repeated all-elided uses still consume the finite work budget")
				}
				steps = maxValueFlowSteps
				assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0) == nil)
				assert.Equal(t, steps, maxValueFlowSteps, "cached exhaustion cannot certify a partial component")
				assert.Equal(t, len(l.valueFlow.active), 0)
				assert.Equal(t, len(l.valueFlow.receivers.active), 0)
			}
		})
	}
}

func TestElidedContainerOmissionsSpendWorkWithoutInventingBindings(t *testing.T) {
	t.Parallel()
	l, _ := valueFlowProbe(t, "const root = [1, 2, 3]; const [, , ,] = root; const probe = root[0]; probe;")
	var literal, pattern *ast.Node
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindVariableDeclaration {
			name := node.Name()
			if name.Kind == ast.KindIdentifier && name.Text() == "root" {
				literal = node.Initializer()
			} else if name.Kind == ast.KindArrayBindingPattern {
				pattern = name
			}
		}
		node.ForEachChild(visit)
		return false
	}
	for _, file := range l.valueFlow.files {
		file.AsNode().ForEachChild(visit)
	}
	assert.Assert(t, literal != nil && pattern != nil)
	treeSteps := maxValueFlowSteps
	tree := l.containerTree(literal, &treeSteps, 0)
	assert.Assert(t, tree != nil)
	contract := l.checker.GetWidenedType(l.checker.GetTypeAtLocation(literal))
	assert.Assert(t, l.checker.IsArrayType(contract), "the guard must exercise an ordinary-array contract, not a rejected tuple")
	const exactBudget = 4 // one pattern entry plus three omitted elements
	for _, budget := range []int{exactBudget - 1, exactBudget} {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			steps, bindings := budget, 0
			closed := l.containerPatternUse(pattern, tree, contract, false, &steps, 0, func(*ast.Node, *ast.Symbol, *literalContainer, int) bool {
				bindings++
				return true
			})
			assert.Check(t, closed == (budget == exactBudget), "only a budget covering the pattern and every omission can certify it")
			assert.Equal(t, steps, 0, "each omission consumes its own bounded step even though it binds no identifier")
			assert.Equal(t, bindings, 0, "elisions never invent an alias or call the binding publication callback")
		})
	}
}

func TestElidedContainerShallowFanoutDoesNotAccumulateDepth(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		var body strings.Builder
		body.WriteString("const root = [{ value: first }, { value: second }];\n")
		for i := range maxValueFlowDepth + 12 {
			fmt.Fprintf(&body, "const [, alias%d] = root; const ignored%d = alias%d.value; const [, ,] = root;\n", i, i, i)
		}
		body.WriteString("const probe = root[1].value; probe;")
		l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+body.String())
		steps := maxValueFlowSteps
		sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)
		assert.Assert(t, sources != nil, "independent elided patterns spend work, not cumulative recursion depth")
		assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"second"}, receivers))
		assert.Assert(t, steps > 0)
	}
}

func elidedContainerUseSource(receivers bool, lateUse string) string {
	return nestedContainerPrelude(receivers) + `const root = { rows: [{ value: first, other: first }, { value: first, other: first }, { value: first, other: first }, { value: first, other: first }, { value: first, other: first }], unselected: { value: first } };
const rootAlias = root; const [, extracted, , sibling] = rootAlias.rows; const other = extracted;
const [, , , , ,] = root.rows;
const check_elided = root.rows[0].value; const check_root_alias = rootAlias.rows[1].value;
const check_extracted = extracted.value; const check_alias = other.value;
const check_sibling = sibling.value; const check_suffix = root.rows[4].value; const check_unselected = root.unselected.value;
const probe = check_extracted; probe;
` + lateUse
}
