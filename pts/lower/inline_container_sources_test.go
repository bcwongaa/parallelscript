package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestInlineContainerSourcesResolveSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"original_inline_root", "const extracted = ({ child: { value: first } }).child; const probe = extracted.value; probe;", []string{"first"}},
				{"object_object", "const extracted = ({ child: { value: first, other: second } }).child; const probe = extracted.value; probe;", []string{"first"}},
				{"object_array", "const extracted = ({ child: [first, second] }).child; const probe = extracted[1]; probe;", []string{"second"}},
				{"array_object", "const extracted = [{ value: first }, { value: second }][1]; const probe = extracted.value; probe;", []string{"second"}},
				{"array_array", "const extracted = [[first, second], [second, first]][1]; const probe = extracted[1]; probe;", []string{"first"}},
				{"mixed_path", "const extracted = ({ branch: [{ row: { child: [first, second] } }] }).branch[0].row.child; const probe = extracted[0]; probe;", []string{"first"}},
				{"aliases", "const extracted = ({ child: { value: first } }).child; const alias = extracted; const other = alias; const probe = other.value; probe;", []string{"first"}},
				{"recursive_extraction", "const extracted = ({ branch: { rows: [{ value: first }, { value: second }] } }).branch; const rows = extracted.rows; const row = rows[1]; const alias = row; const probe = alias.value; probe;", []string{"second"}},
				{"parentheses", "const extracted = ((({ child: ({ value: first }) }).child)); const alias = ((extracted)); const probe = (alias).value; probe;", []string{"first"}},
				{"string_keys", "const extracted = ({ child: [{ value: first }] })[\"child\"][\"0\"]; const probe = extracted[\"value\"]; probe;", []string{"first"}},
				{"readonly_extraction", "const extracted: Readonly<{ value: Value }> = ({ child: { value: first } }).child; const probe = extracted.value; probe;", []string{"first"}},
				{"readonly_data_alias", "const extracted = ({ child: { value: first } }).child; const alias: Readonly<{ value: Value }> = extracted; const probe = alias.value; probe;", []string{"first"}},
				{"terminal_alias", "const selected = first; const extracted = ({ child: { value: selected } }).child; const terminal = extracted.value; const probe = terminal; probe;", []string{"first"}},
				{"terminal_return", "function selected(): Value { return first; } const extracted = ({ child: { value: selected() } }).child; const probe = extracted.value; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { const extracted = ({ child: { value: first } }).child; const alias = extracted; return alias.value; } const probe = selected(); probe;", []string{"first"}},
				{"closed_terminal_parameter", "function selected(value: Value): void { const extracted = ({ child: { value } }).child; const probe = extracted.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"terminal_union", "const extracted = ({ child: { value: choose() ? first : second } }).child; const probe = extracted.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps

					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)

					assert.Assert(t, sources != nil, "the fresh inline literal and complete extracted component prove a selected terminal")
					assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames(tt.want, receivers))
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				})
			}
		})
	}
}

func TestInlineContainerLateUsesPoisonEveryExtractedAlias(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"unmodified", ""},
				{"selected_write", "other.value = second;"},
				{"unselected_write", "other.other = second;"},
				{"sibling_write", "sibling.value = second;"},
				{"parent_write", "alias.plain = second;"},
				{"intermediate_replacement", "extracted.main = extracted.side;"},
				{"captured_write", "function change(): void { other.value = second; }"},
				{"asserted_container_write", "(other as { value: Value }).value = second;"},
				{"asserted_target_write", "(other.value as Value) = second;"},
				{"pattern_write", "({ value: other.value } = { value: second });"},
				{"for_of_write", "for (other.value of [second]) {}"},
				{"delete", "delete (other as any).value;"},
				{"update", "other.count++;"},
				{"passed_leaf", "consume(other);"},
				{"passed_parent", "consume(extracted);"},
				{"returned_sibling", "function expose(): unknown { return sibling; }"},
				{"stored_leaf", "const stored = [other];"},
				{"stored_sibling", "const stored = { sibling };"},
				{"spread_leaf", "const copied = { ...other };"},
				{"mutable_alias", "let mutable = other;"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"unknown_alias", "const opaque: unknown = other;"},
				{"asserted_alias", "const asserted = other as { value: Value };"},
				{"nonnull_alias", "const asserted = other!;"},
				{"satisfies_alias", "const asserted = other satisfies { value: Value };"},
				{"conditional_alias", "const merged = choose() ? other : sibling;"},
				{"comma_alias", "const merged = (choose(), other);"},
				{"destructured_leaf", "const { value } = other;"},
				{"exported_leaf", "export const outward = other;"},
				{"standalone_export", "export { other };"},
				{"dynamic_key", "let key: \"value\" = \"value\"; const ignored = other[key];"},
				{"asserted_read", "const ignored = (other as { value: Value }).value;"},
				{"missing_key", "// @ts-ignore: defensive missing-key source guard\nconst ignored = other[\"absent\"];"},
				{"implicit_this", "other.replace();"},
				{"parenthesized_implicit_this", "(other.replace)();"},
				{"optional_implicit_this", "other.replace?.();"},
				{"asserted_implicit_this", "(other.replace as typeof replace)();"},
				{"instantiated_implicit_this", "(other.replace<number>)(1);"},
				{"tag_implicit_this", "other.replace``;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					for _, reverse := range []bool{false, true} {
						t.Run(fmt.Sprintf("reverse_%t", reverse), func(t *testing.T) {
							t.Parallel()
							// Explicit-this and wrapped writes test proof conservatism,
							// not support for those forms in native lowering.
							l, _ := valueFlowProbe(t, inlineContainerUseSource(receivers, tt.use))
							projections := containerAliasProbeExpressions(l)
							assert.Equal(t, len(projections), 6)
							if reverse {
								slices.Reverse(projections)
							}
							for _, projection := range projections {
								steps := maxValueFlowSteps
								sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
								if tt.use == "" {
									assert.Assert(t, sources != nil, "the identical unmodified fixture must reach a positive proof")
									assert.DeepEqual(t, returnedSourceNames(sources), inlineContainerSourceNames([]string{"first"}, receivers))
								} else {
									assert.Assert(t, sources == nil, "a late unsafe alias or sibling use invalidates every selected subtree in either cache order")
								}
							}
							assert.Equal(t, len(l.valueFlow.active), 0)
							assert.Equal(t, len(l.valueFlow.receivers.active), 0)
						})
					}
				})
			}
		})
	}
}

func TestInlineContainerArrayUsesRequireCompleteClosure(t *testing.T) {
	t.Parallel()
	for _, use := range []string{
		"", "alias[0] = second;", "alias.reverse();", "alias.length = 0;", "const ignored = alias.length;",
		"let index = 0; const ignored = alias[index];", "consume(alias);", "const copied = [...alias];",
		"const opaque: unknown = alias;", "const asserted = alias as Value[];", "let mutable = alias;",
	} {
		name := use
		if name == "" {
			name = "unmodified"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, _ := valueFlowProbe(t, nestedContainerPrelude(false)+`const extracted = ({ child: [first, second] }).child; const alias = extracted;
const check_extracted = extracted[0]; const check_alias = alias[0]; const probe = check_extracted; probe;
`+use)
			for _, projection := range containerAliasProbeExpressions(l) {
				steps := maxValueFlowSteps
				sources := l.valueSources(projection, &steps, 0)
				if use == "" {
					assert.Assert(t, sources != nil, "shared Value terminal contracts make the safe array proof reachable")
					assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
				} else {
					assert.Assert(t, sources == nil)
				}
			}
		})
	}
}

func TestInlineContainerDeclaredAliasContractsCannotBeNarrowedAway(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		contract string
		known    bool
	}{
		{"same_data", "{ value: Value }", true},
		{"readonly_data", "Readonly<{ value: Value }>", true},
		{"any_member", "{ value: any }", false},
		{"unknown_member", "{ value: unknown }", false},
		{"optional_member", "{ value?: Value }", false},
		{"function_to_method", "{ value(): void }", false},
		{"accessor_member", "{ get value(): Value }", false},
		{"narrowed_union", "{ value: Value } | { value: Value; tag: number }", false},
		{"primitive_union", "{ value: Value } | number", false},
		{"index_signature", "{ [key: string]: Value }", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := "const extracted = ({ child: { value: first } }).child; const alias: " + tt.contract + " = extracted; const probe = extracted.value; probe;"
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps := maxValueFlowSteps
			sources := l.valueSources(probe, &steps, 0)
			assert.Equal(t, sources != nil, tt.known, "screen declared alias contracts, not the apparent type of its initialized read")
			if tt.known {
				assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
			}
		})
	}
	for _, contract := range []string{"Value[]", "readonly Value[]", "any[]", "unknown[]"} {
		t.Run(contract, func(t *testing.T) {
			t.Parallel()
			body := "const extracted = ({ child: [first, second] }).child; const alias: " + contract + " = extracted; const probe = extracted[0]; probe;"
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps := maxValueFlowSteps
			sources := l.valueSources(probe, &steps, 0)
			assert.Equal(t, sources != nil, contract == "Value[]", "an array contract change is not a data-only object alias")
		})
	}
}

func TestInlineContainerUnsupportedRootsAndUnselectedMembersStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"let extracted = ({ child: { value: first } }).child; const probe = extracted.value; probe;",
		"export const extracted = ({ child: { value: first } }).child; const probe = extracted.value; probe;",
		"const extracted = (({ child: { value: first } }) as { child: { value: Value } }).child; const probe = extracted.value; probe;",
		"const extracted = (({ child: { value: first } }) satisfies { child: { value: Value } }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: { value: first } }).child!; const probe = extracted.value; probe;",
		"const extracted: { value(): void } = ({ child: { value: first } }).child; const probe = extracted.value; probe;",
		"const extracted: { value: any } = ({ child: { value: first } }).child; const probe = extracted.value; probe;",
		"const inner = { value: first }; const extracted = ({ child: inner }).child; const probe = extracted.value; probe;",
		"function inner(): { value: Value } { return { value: first }; } const extracted = ({ child: inner() }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: choose() ? { value: first } : { value: second } }).child; const probe = extracted.value; probe;",
		"function select(): { child: { value: Value } } { return { child: { value: first } }; } const extracted = select().child; const probe = extracted.value; probe;",
		"function select(root: { child: { value: Value } }): void { const extracted = root.child; const probe = extracted.value; probe; } select({ child: { value: first } });",
		"const extracted = ({ child: { value: first }, invalid: { get value(): Value { return second; } } }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: { value: first }, invalid: { value(): void {} } }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: { value: first }, invalid: [first, , second] }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: { value: first }, invalid: { ...{ value: second } } }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: { value: first }, invalid: { [\"value\"]: second } }).child; const probe = extracted.value; probe;",
		"const extracted = ({ child: { value: first }, invalid: { __proto__: { value: second } } }).child; const probe = extracted.value; probe;",
		"function outer<T>(value: T): void { const extracted = ({ child: { value: first } }).child; const probe = extracted.value; probe; } outer(1);",
		"const outer: <T>(value: T) => void = value => { const extracted = ({ child: { value: first } }).child; const probe = extracted.value; probe; }; outer(1);",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
		})
	}
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("distinct_array_contracts_%t", receivers), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(receivers)+"const extracted = ({ child: [first, second] }).child; const probe = extracted[0]; probe;")
			steps := maxValueFlowSteps
			assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0) == nil, "an inline root cannot bypass distinct nonliteral array-element contracts")
		})
	}
}

func TestInlineContainerFanoutConsumesWorkNotAccumulatedDepth(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+inlineContainerFanout(maxValueFlowDepth+12)+"const probe = extracted.value; probe;")
	steps := maxValueFlowSteps
	sources := l.valueSources(probe, &steps, 0)
	assert.Assert(t, sources != nil)
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	assert.Assert(t, steps > 0)
}

func TestInlineContainerBoundsCannotPublishPartialPositiveFacts(t *testing.T) {
	t.Parallel()
	var deep strings.Builder
	deep.WriteString("const extracted = ({ child: { value: first } }).child; const alias0 = extracted;\n")
	for i := 1; i <= maxValueFlowDepth; i++ {
		fmt.Fprintf(&deep, "const alias%d = alias%d;\n", i, i-1)
	}
	fmt.Fprintf(&deep, "const probe = alias%d.value; probe;", maxValueFlowDepth)
	for _, tt := range []struct {
		name  string
		body  string
		steps int
	}{
		{"depth", deep.String(), maxValueFlowSteps},
		{"reference_work", inlineContainerFanout(maxValueFlowSteps+1) + "const probe = extracted.value; probe;", maxValueFlowSteps},
		{"transitive_work", "const extracted = ({ child: { value: first } }).child; const alias = extracted; " + strings.Repeat("alias.value;", maxValueFlowSteps+1) + "const probe = extracted.value; probe;", maxValueFlowSteps},
		{"small_budget", "const extracted = ({ child: { value: first } }).child; const alias = extracted; const probe = alias.value; probe;", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+tt.body)
			steps := tt.steps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			if tt.name == "depth" {
				assert.Assert(t, steps > 0)
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, steps, maxValueFlowSteps, "cached unknown must not rerun an exhausted component")
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestInlineContainerLiteralDepthAndWholeRootWorkStayBounded(t *testing.T) {
	t.Parallel()
	const nestedDepth = 40
	for _, tt := range []struct {
		name  string
		body  string
		depth int
	}{
		{"literal_depth", "const extracted = (" + strings.Repeat("{ branch: ", nestedDepth) + "{ value: first }" + strings.Repeat(" }", nestedDepth) + ").branch; const probe = extracted" + strings.Repeat(".branch", nestedDepth-1) + ".value; probe;", maxValueFlowDepth - nestedDepth},
		{"unselected_root_work", "const extracted = ({ child: { value: first }, wide: [" + strings.Repeat("first,", maxValueFlowSteps+1) + "] }).child; const probe = extracted.value; probe;", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+tt.body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, tt.depth) == nil)
			if tt.name == "literal_depth" {
				assert.Assert(t, steps > 0, "depth, not accumulated work, stops the path")
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Equal(t, steps, maxValueFlowSteps)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestInlineContainerExhaustionDoesNotCertifyUncheckedSiblings(t *testing.T) {
	t.Parallel()
	l, _ := valueFlowProbe(t, inlineContainerUseSource(false, "consume(sibling);"))
	projections := containerAliasProbeExpressions(l)
	assert.Equal(t, len(projections), 6)
	steps := 12
	assert.Assert(t, l.valueSources(projections[0], &steps, 0) == nil)
	for _, projection := range projections {
		steps = maxValueFlowSteps
		assert.Assert(t, l.valueSources(projection, &steps, 0) == nil)
	}
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func TestInlineContainerCyclesStayUnknown(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+"function selected(): Value { return extracted.value; } const extracted = ({ child: { value: selected() } }).child; const probe = extracted.value; probe;")
	steps := maxValueFlowSteps
	assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
	assert.Assert(t, steps > 0)
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func TestInlineContainerFunctionReceiverAndIndependentRootCachesStaySeparate(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		for _, receiverFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse_%t_receiver_first_%t", reverse, receiverFirst), func(t *testing.T) {
				t.Parallel()
				l, _ := valueFlowProbe(t, nestedContainerPrelude(true)+`const poisoned = ({ child: { value: first } }).child;
const safe = ({ child: { value: second } }).child;
const check_poisoned = poisoned.value; const check_safe = safe.value;
consume(poisoned); const probe = check_safe; probe;`)
				projections := containerAliasProbeExpressions(l)
				assert.Equal(t, len(projections), 2)
				if reverse {
					slices.Reverse(projections)
				}
				for range 2 {
					for _, projection := range projections {
						functions := func() {
							steps := maxValueFlowSteps
							assert.Assert(t, l.valueSources(projection, &steps, 0) == nil)
						}
						receivers := func() {
							steps := maxValueFlowSteps
							sources := l.receiverSources(projection, &steps, 0)
							if projection.Expression().Text() == "safe" {
								assert.Assert(t, sources != nil)
								assert.DeepEqual(t, returnedSourceNames(sources), []string{"B"})
							} else {
								assert.Assert(t, sources == nil)
							}
						}
						if receiverFirst {
							receivers()
							functions()
						} else {
							functions()
							receivers()
						}
					}
				}
				assert.Equal(t, len(l.valueFlow.active), 0)
				assert.Equal(t, len(l.valueFlow.receivers.active), 0)
			})
		}
	}
}

func inlineContainerSourceNames(names []string, receivers bool) []string {
	if !receivers {
		return names
	}
	result := make([]string, len(names))
	for i, name := range names {
		if name == "first" {
			result[i] = "A"
		} else {
			result[i] = "B"
		}
	}
	return result
}

func inlineContainerUseSource(receivers bool, lateUse string) string {
	return nestedContainerPrelude(receivers) + `function replace<T>(this: { value: Value }, input?: T): void { this.value = second; }
const extracted = ({ child: { main: { value: first, other: first, count: 0, replace }, side: { value: first, other: first, count: 0, replace }, plain: first }, ignored: { value: second } }).child;
const alias = extracted; const leaf = alias.main; const other = leaf; const sibling = extracted.side;
const check_extracted = extracted.main.value; const check_alias = alias.main.value;
const check_leaf = leaf.value; const check_other = other.value;
const check_sibling = sibling.value; const check_plain = extracted.plain;
const probe = check_extracted; probe;
` + lateUse
}

func inlineContainerFanout(count int) string {
	var source strings.Builder
	source.WriteString("const extracted = ({ child: { value: first } }).child;\n")
	for i := range count {
		fmt.Fprintf(&source, "const alias%d = extracted; const selected%d = alias%d.value;\n", i, i, i)
	}
	return source.String()
}
