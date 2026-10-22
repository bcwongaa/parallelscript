package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestExtractedContainerSourcesResolveSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"object_child", "const root = { child: { value: first, other: second } }; const extracted = root.child; const probe = extracted.value; probe;", []string{"first"}},
				{"array_child", "const root = { child: [first, second] }; const extracted = root.child; const probe = extracted[1]; probe;", []string{"second"}},
				{"array_object", "const root = [{ value: first }, { value: second }]; const extracted = root[1]; const probe = extracted.value; probe;", []string{"second"}},
				{"array_array", "const root = [[first, second], [second, first]]; const extracted = root[1]; const probe = extracted[1]; probe;", []string{"first"}},
				{"mixed_path", "const root = { branch: [{ row: { child: [first, second] } }] }; const extracted = root.branch[0].row.child; const probe = extracted[0]; probe;", []string{"first"}},
				{"root_alias", "const root = { child: { value: first } }; const alias = root; const other = alias; const extracted = other.child; const probe = extracted.value; probe;", []string{"first"}},
				{"child_alias", "const root = { child: { value: first } }; const extracted = root.child; const alias = extracted; const other = alias; const probe = other.value; probe;", []string{"first"}},
				{"recursive_extraction", "const root = { branch: { rows: [{ value: first }, { value: second }] } }; const branch = root.branch; const branchAlias = branch; const rows = branchAlias.rows; const row = rows[1]; const alias = row; const probe = alias.value; probe;", []string{"second"}},
				{"parentheses", "const root = ({ child: ({ value: first }) }); const extracted = (((root).child)); const alias = ((extracted)); const probe = (alias).value; probe;", []string{"first"}},
				{"string_keys", "const root = { child: [{ value: first }] }; const extracted = root[\"child\"][\"0\"]; const probe = extracted[\"value\"]; probe;", []string{"first"}},
				{"root_projection_after_extraction", "const root = { child: { value: first }, plain: second }; const extracted = root.child; const ignored = extracted.value; const probe = root.plain; probe;", []string{"second"}},
				{"sibling_extractions", "const root = { child: { value: first }, side: { value: second } }; const extracted = root.child; const sibling = root.side; const ignored = sibling.value; const probe = extracted.value; probe;", []string{"first"}},
				{"fanout_same_child", "const root = { child: { value: first, other: second } }; const alias = root; const extracted = root.child; const sibling = alias.child; const other = extracted; const ignored = sibling.other; const probe = other.value; probe;", []string{"first"}},
				{"readonly_data_alias", "const root = { child: { value: first, other: second } }; const extracted: Readonly<{ value: Value; other: Value }> = root.child; const probe = extracted.value; probe;", []string{"first"}},
				{"terminal_alias", "const selected = first; const root = { child: { value: selected } }; const extracted = root.child; const terminal = extracted.value; const probe = terminal; probe;", []string{"first"}},
				{"terminal_return", "function selected(): Value { return first; } const root = { child: { value: selected() } }; const extracted = root.child; const probe = extracted.value; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { const root = { child: { value: first } }; const extracted = root.child; return extracted.value; } const probe = selected(); probe;", []string{"first"}},
				{"closed_terminal_parameter", "function selected(value: Value): void { const root = { child: { value } }; const extracted = root.child; const probe = extracted.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"terminal_union", "const root = { child: { value: choose() ? first : second } }; const extracted = root.child; const probe = extracted.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps

					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)

					assert.Assert(t, sources != nil, "a closed named literal root and its extracted child aliases prove the selected terminal")
					want := tt.want
					if receivers {
						want = make([]string, len(tt.want))
						for i, name := range tt.want {
							if name == "first" {
								want[i] = "A"
							} else {
								want[i] = "B"
							}
						}
					}
					assert.DeepEqual(t, returnedSourceNames(sources), want)
					assert.Equal(t, len(l.valueFlow.active), 0)
					assert.Equal(t, len(l.valueFlow.receivers.active), 0)
				})
			}
		})
	}
}

func TestExtractedContainerUsesPoisonEveryRootAndChildAlias(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"unmodified", ""},
				{"root_write", "root.child.value = second;"},
				{"extracted_write", "extracted.value = second;"},
				{"transitive_write", "other.value = second;"},
				{"unselected_write", "other.other = second;"},
				{"sibling_write", "sibling.value = second;"},
				{"root_plain_write", "alias.plain = second;"},
				{"intermediate_replacement", "alias.child = alias.side;"},
				{"captured_write", "function change(): void { other.value = second; }"},
				{"asserted_container_write", "(other as { value: Value }).value = second;"},
				{"asserted_target_write", "(other.value as Value) = second;"},
				{"pattern_write", "({ value: other.value } = { value: second });"},
				{"for_of_write", "for (other.value of [second]) {}"},
				{"delete", "delete (other as any).value;"},
				{"update", "other.count++;"},
				{"passed_child", "consume(other);"},
				{"returned_child", "function expose(): unknown { return other; }"},
				{"stored_child", "const stored = [other];"},
				{"stored_sibling", "const stored = { sibling };"},
				{"spread_child", "const copied = { ...other };"},
				{"mutable_alias", "let mutable = other;"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"unknown_alias", "const dynamic: unknown = other;"},
				{"asserted_alias", "const asserted = other as { value: Value };"},
				{"nonnull_alias", "const asserted = other!;"},
				{"satisfies_alias", "const asserted = other satisfies { value: Value };"},
				{"conditional_alias", "const merged = choose() ? other : sibling;"},
				{"comma_alias", "const merged = (choose(), other);"},
				{"destructured_child", "const { value } = other;"},
				{"exported_child", "export const outward = other;"},
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
							// Wrapped writes and explicit-this syntax exercise the proof,
							// not a claim that native lowering supports those forms.
							source := nestedContainerPrelude(receivers) + `function replace<T>(this: { value: Value }, input?: T): void { this.value = second; }
const root = { child: { value: first, other: first, count: 0, replace }, side: { value: first, other: first, count: 0, replace }, plain: first };
const alias = root; const extracted = alias.child; const other = extracted; const sibling = root.side;
` + tt.use + extractedContainerProbeDeclarations()
							l, _ := valueFlowProbe(t, source)
							projections := containerAliasProbeExpressions(l)
							assert.Equal(t, len(projections), 6)
							if reverse {
								slices.Reverse(projections)
							}
							for _, projection := range projections {
								steps := maxValueFlowSteps
								sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
								if tt.use == "" {
									assert.Assert(t, sources != nil, "the unmodified same fixture must reach a positive proof")
									want := "first"
									if receivers {
										want = "A"
									}
									assert.DeepEqual(t, returnedSourceNames(sources), []string{want})
								} else {
									assert.Assert(t, sources == nil, "one unsafe root, extracted child or sibling use poisons every projection, independent of cache order")
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

func TestExtractedContainerArraysCheckEveryUse(t *testing.T) {
	t.Parallel()
	for _, use := range []string{
		"", "other[0] = second;", "other.reverse();", "other.length = 0;", "const ignored = other.length;",
		"let index = 0; const ignored = other[index];", "consume(other);", "const copied = [...other];",
		"const opaque: unknown = other;", "const asserted = other as Value[];", "let mutable = other;",
	} {
		name := use
		if name == "" {
			name = "unmodified"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := nestedContainerPrelude(false) + "const root = { child: [first, second], plain: first }; const extracted = root.child; const other = extracted;\n" + use + `
const check_root = root.child[0]; const check_extracted = extracted[0]; const check_other = other[0]; const check_plain = root.plain;
const probe = check_root; probe;`
			l, _ := valueFlowProbe(t, source)
			projections := containerAliasProbeExpressions(l)
			assert.Equal(t, len(projections), 4)
			for _, projection := range projections {
				steps := maxValueFlowSteps
				sources := l.valueSources(projection, &steps, 0)
				if use == "" {
					assert.Assert(t, sources != nil)
					assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
				} else {
					assert.Assert(t, sources == nil)
				}
			}
		})
	}
}

func TestExtractedContainerUnsupportedOriginsAndContractsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"const root = { child: { value: first } }; const { child: extracted } = root; const probe = extracted.value; probe;",
		"let root = { child: { value: first } }; const extracted = root.child; const probe = extracted.value; probe;",
		"const root: any = { child: { value: first } }; const extracted = root.child; const probe = extracted.value; probe;",
		"const root = { child: { value: first } } as { child: { value: Value } }; const extracted = root.child; const probe = extracted.value; probe;",
		"const inner = { value: first }; const root = { child: inner }; const extracted = root.child; const probe = extracted.value; probe;",
		"function inner(): { value: Value } { return { value: first }; } const root = { child: inner() }; const extracted = root.child; const probe = extracted.value; probe;",
		"const root = { child: choose() ? { value: first } : { value: second } }; const extracted = root.child; const probe = extracted.value; probe;",
		"function selected(root: { child: { value: Value } }): void { const extracted = root.child; const probe = extracted.value; probe; } selected({ child: { value: first } });",
		"function selected(): { child: { value: Value } } { return { child: { value: first } }; } const root = selected(); const extracted = root.child; const probe = extracted.value; probe;",
		"const root = { child: { value: first } }; const extracted: { value: any } = root.child; const probe = root.child.value; probe;",
		"const root = { child: { value: first } }; const extracted: { value: unknown } = root.child; const probe = root.child.value; probe;",
		"const root = { child: { value: first } }; const extracted: { value?: Value } = root.child; const probe = root.child.value; probe;",
		"const root = { child: { value: first } }; const extracted: { value(): void } = root.child; const probe = root.child.value; probe;",
		"interface Child { get value(): Value; } const root = { child: { value: first } }; const extracted: Child = root.child; const probe = root.child.value; probe;",
		"const root = { child: { value: first } }; const extracted: { value: Value } | { value: Value; tag: number } = root.child; const probe = root.child.value; probe;",
		"const root = { child: [first, second] }; const extracted: readonly Value[] = root.child; const probe = root.child[0]; probe;",
		"const root: { child: [Value, Value] } = { child: [first, second] }; const extracted: Value[] = root.child; const probe = root.child[0]; probe;",
		"const root = { child: { value: first }, invalid: { get value(): Value { return second; } } }; const extracted = root.child; const probe = extracted.value; probe;",
		"const root = { child: { value: first }, invalid: [first, , second] }; const extracted = root.child; const probe = extracted.value; probe;",
		"function outer<T>(value: T): void { const root = { child: { value: first } }; const extracted = root.child; const probe = extracted.value; probe; } outer(1);",
		"const outer: <T>(value: T) => void = value => { const root = { child: { value: first } }; const extracted = root.child; const probe = extracted.value; probe; }; outer(1);",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "unsupported origin or changed checker contract is unknown for the whole component")
		})
	}
}

func TestExtractedContainerDistinctArrayTerminalContractsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(receivers)+"const root = { child: [first, second] }; const extracted = root.child; const probe = extracted[0]; probe;")
			steps := maxValueFlowSteps
			assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0) == nil, "extraction cannot bypass the raw common-element contract barrier")
		})
	}
}

func TestExtractedContainerFanoutUsesWorkNotAccumulatedDepth(t *testing.T) {
	t.Parallel()
	l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+extractedContainerFanout(maxValueFlowDepth+12)+"const probe = root.child.value; probe;")
	steps := maxValueFlowSteps

	sources := l.valueSources(probe, &steps, 0)

	assert.Assert(t, sources != nil, "independent shallow child extractions are not one deep path")
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	assert.Assert(t, steps > 0)
}

func TestExtractedContainerBoundsDoNotPublishPartialPositiveFacts(t *testing.T) {
	t.Parallel()
	var deep strings.Builder
	deep.WriteString("const root = { child: { value: first } }; const extracted = root.child; const alias0 = extracted;\n")
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
		{"reference_work", extractedContainerFanout(maxValueFlowSteps+1) + "const probe = root.child.value; probe;", maxValueFlowSteps},
		{"transitive_work", "const root = { child: { value: first } }; const extracted = root.child; const other = extracted; " + strings.Repeat("other.value;", maxValueFlowSteps+1) + "const probe = root.child.value; probe;", maxValueFlowSteps},
		{"small_budget", "const root = { child: { value: first } }; const extracted = root.child; const other = extracted; const probe = other.value; probe;", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+tt.body)
			steps := tt.steps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			if tt.name == "depth" {
				assert.Assert(t, steps > 0, "depth rather than total work stopped the chain")
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "cached exhaustion stays unknown")
			assert.Equal(t, steps, maxValueFlowSteps)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestExtractedContainerExhaustionCannotPublishUncheckedSiblingFacts(t *testing.T) {
	t.Parallel()
	l, _ := valueFlowProbe(t, nestedContainerPrelude(false)+extractedContainerFanout(maxValueFlowDepth+12)+`
const alias = root; const extracted = alias.child; const other = extracted; const sibling = root.side;
consume(sibling);
`+extractedContainerProbeDeclarations())
	projections := containerAliasProbeExpressions(l)
	assert.Equal(t, len(projections), 6)
	steps := 12
	assert.Assert(t, l.valueSources(projections[0], &steps, 0) == nil)
	for _, projection := range projections {
		steps = maxValueFlowSteps
		assert.Assert(t, l.valueSources(projection, &steps, 0) == nil, "an exhausted traversal must not certify any alias before finding an escaped sibling")
	}
	assert.Equal(t, len(l.valueFlow.active), 0)
}

func TestExtractedContainerCyclesStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"function selected(): Value { return extracted.value; } const root = { child: { value: selected() } }; const extracted = root.child; const probe = extracted.value; probe;",
		"// @ts-ignore: defensive source-only forward projection cycle\nconst root: { child: { value: Value } } = { child: extracted }; const extracted: { value: Value } = root.child; const probe = extracted.value; probe;",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, nestedContainerPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Assert(t, steps > 0)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestExtractedContainerFunctionAndReceiverCachesStaySeparate(t *testing.T) {
	t.Parallel()
	for _, receiverFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(receiverFirst), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} const root = { branch: [{ value: new A() }] }; const branch = root.branch; const row = branch[0]; const alias = row; const probe = alias.value; probe;")
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

func extractedContainerProbeDeclarations() string {
	return `
const check_root = root.child.value; const check_alias = alias.child.value;
const check_extracted = extracted.value; const check_other = other.value;
const check_sibling = sibling.value; const check_plain = root.plain;
const probe = check_root; probe;
`
}

func extractedContainerFanout(count int) string {
	var source strings.Builder
	source.WriteString("const root = { child: { value: first }, side: { value: first }, plain: first };\n")
	for i := range count {
		fmt.Fprintf(&source, "const child%d = root.child; const selected%d = child%d.value;\n", i, i, i)
	}
	return source.String()
}
