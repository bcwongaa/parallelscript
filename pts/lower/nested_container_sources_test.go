package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNestedContainerSourcesResolveSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"fresh_object", "const probe = ({ branch: { value: first, other: second } }).branch.value; probe;", []string{"first"}},
				{"fresh_array", "const probe = [[first, second]][0][1]; probe;", []string{"second"}},
				{"object_object", "const root = { branch: { value: first, other: second }, sibling: { value: second } }; const probe = root.branch.value; probe;", []string{"first"}},
				{"object_array", "const root = { branch: [first, second] }; const probe = root.branch[1]; probe;", []string{"second"}},
				{"array_object", "const root = [{ value: first }, { value: second }]; const probe = root[0].value; probe;", []string{"first"}},
				{"array_array", "const root = [[first, second], [second, first]]; const probe = root[1][1]; probe;", []string{"first"}},
				{"mixed_tree", "const root = { branch: [{ row: [first, second] }, { row: [second, first] }] }; const probe = root.branch[1].row[0]; probe;", []string{"second"}},
				{"constant_string_keys", "const root = { branch: [{ value: first }] }; const probe = root[\"branch\"][\"0\"][\"value\"]; probe;", []string{"first"}},
				{"root_aliases", "const root = { branch: [{ value: first, other: second }] }; const alias = root; const other = alias; const probe = other.branch[0].value; probe;", []string{"first"}},
				{"root_fanout", "const root = { branch: { value: first, other: second }, sibling: [second] }; const alias = root; const other = alias; const sibling = root; const ignored = sibling.sibling[0]; const probe = other.branch.value; probe;", []string{"first"}},
				{"parentheses", "const root = ({ branch: ({ row: [first, second] }) }); const alias = ((root)); const probe = (((alias).branch).row)[0]; probe;", []string{"first"}},
				{"terminal_alias", "const selected = first; const again = selected; const root = { branch: { value: again } }; const probe = root.branch.value; probe;", []string{"first"}},
				{"terminal_projection_alias", "const root = { branch: { value: first } }; const selected = root.branch.value; const again = selected; const probe = again; probe;", []string{"first"}},
				{"terminal_return", "function selected(): Value { return first; } const root = { branch: { value: selected() } }; const probe = root.branch.value; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { const root = { branch: { value: first } }; const alias = root; return alias.branch.value; } const probe = selected(); probe;", []string{"first"}},
				{"closed_parameter", "function selected(value: Value): void { const root = { branch: { value } }; const alias = root; const probe = alias.branch.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"terminal_union", "const root = { branch: { value: choose() ? first : second } }; const probe = root.branch.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, nestedContainerPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps

					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)

					assert.Assert(t, sources != nil, "a closed direct data-literal tree proves its selected terminal")
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

// D-211 makes these unchanged D-210 unknown controls closed components.
func TestNestedContainerExtractedAliasesRemainClosed(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name   string
				source string
			}{
				{"root_component", nestedContainerPrelude(receivers) + `function replace<T>(this: { value: Value }, input?: T): void { this.value = second; }
const root = { branch: { value: first, other: first, count: 0, replace }, side: { value: first, other: first, count: 0, replace }, plain: first };
const alias = root; const other = alias; const sibling = root;
const extracted = other.branch;` + nestedContainerProbeDeclarations(".branch.value")},
				{"array_component", nestedContainerPrelude(receivers) + "const root = { branch: [first, second], plain: first }; const alias = root; const other = alias;\nconst extracted = other.branch;" + nestedContainerProbeDeclarations(".branch[0]")},
				{"child_projection", literalProjectionPrelude(receivers) + "const root = { branch: { value: first } }; const inner = root.branch; const probe = inner.value; probe;"},
				{"root_projection", literalProjectionPrelude(receivers) + "const root = { branch: { value: first } }; const inner = root.branch; const probe = root.branch.value; probe;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					for _, reverse := range []bool{false, true} {
						l, probe := valueFlowProbe(t, tt.source)
						projections := containerAliasProbeExpressions(l)
						if len(projections) == 0 {
							projections = append(projections, probe)
						}
						if reverse {
							slices.Reverse(projections)
						}
						for _, projection := range projections {
							steps := maxValueFlowSteps
							sources := (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0)
							want := "first"
							if receivers {
								want = "A"
							}
							assert.DeepEqual(t, returnedSourceNames(sources), []string{want})
						}
					}
				})
			}
		})
	}
}

func TestNestedContainerUnsafeUsesPoisonWholeRoot(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"root_leaf_write", "root.branch.value = second;"},
				{"transitive_leaf_write", "other.branch.value = second;"},
				{"unselected_leaf_write", "other.branch.other = second;"},
				{"sibling_leaf_write", "sibling.side.value = second;"},
				{"intermediate_replacement", "other.branch = other.side;"},
				{"captured_write", "function change(): void { other.branch.value = second; }"},
				{"asserted_container_write", "(other.branch as { value: Value }).value = second;"},
				{"asserted_target_write", "(other.branch.value as Value) = second;"},
				{"pattern_write", "({ value: other.branch.value } = { value: second });"},
				{"for_of_write", "for (other.branch.value of [second]) {}"},
				{"delete", "delete (other.branch as any).value;"},
				{"update", "other.branch.count++;"},
				{"passed_inner", "consume(other.branch);"},
				{"returned_inner", "function expose(): unknown { return other.branch; }"},
				{"stored_inner", "const stored = [other.branch];"},
				{"stored_sibling", "const stored = { nested: sibling.side };"},
				{"spread_inner", "const copied = { ...other.branch };"},
				{"mutable_inner_alias", "let extracted = other.branch;"},
				{"dynamic_inner_alias", "const extracted: any = other.branch;"},
				{"asserted_inner_read", "const ignored = (other.branch as { value: Value }).value;"},
				{"dynamic_inner_key", "let key: \"value\" = \"value\"; const ignored = other.branch[key];"},
				{"implicit_this", "other.branch.replace();"},
				{"parenthesized_implicit_this", "(other.branch.replace)();"},
				{"optional_implicit_this", "other.branch.replace?.();"},
				{"asserted_implicit_this", "(other.branch.replace as typeof replace)();"},
				{"instantiated_implicit_this", "(other.branch.replace<number>)(1);"},
				{"instantiated_optional_implicit_this", "((other.branch.replace<number>))?.(1);"},
				{"tag_implicit_this", "other.branch.replace``;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					for _, reverse := range []bool{false, true} {
						t.Run(fmt.Sprintf("reverse_%t", reverse), func(t *testing.T) {
							t.Parallel()
							// Explicit-this and wrapped assignments are source-proof guards,
							// not claims that native lowering supports those forms.
							source := nestedContainerPrelude(receivers) + `function replace<T>(this: { value: Value }, input?: T): void { this.value = second; }
const root = { branch: { value: first, other: first, count: 0, replace }, side: { value: first, other: first, count: 0, replace }, plain: first };
const alias = root; const other = alias; const sibling = root;
` + tt.use + nestedContainerProbeDeclarations(".branch.value")
							l, _ := valueFlowProbe(t, source)
							projections := containerAliasProbeExpressions(l)
							assert.Equal(t, len(projections), 4)
							if reverse {
								slices.Reverse(projections)
							}
							for _, projection := range projections {
								steps := maxValueFlowSteps
								assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0) == nil, "unsafe intermediate/sibling uses invalidate even a direct scalar sibling, independent of cache order")
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

func TestNestedContainerArraysRejectIntermediateEscapeAndMutation(t *testing.T) {
	t.Parallel()
	for _, use := range []string{
		"", "other.branch[0] = second;", "other.branch.reverse();", "other.branch.length = 0;", "const ignored = other.branch.length;",
		"let index = 0; const ignored = other.branch[index];", "consume(other.branch);", "const copied = [...other.branch];",
		"const extracted: unknown = other.branch;", "const extracted = other.branch as Value[];",
	} {
		name := use
		if name == "" {
			name = "unmodified"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, _ := valueFlowProbe(t, nestedContainerPrelude(false)+"const root = { branch: [first, second], plain: first }; const alias = root; const other = alias;\n"+use+nestedContainerProbeDeclarations(".branch[0]"))
			for _, projection := range containerAliasProbeExpressions(l) {
				steps := maxValueFlowSteps
				sources := l.valueSources(projection, &steps, 0)
				if use == "" {
					assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
				} else {
					assert.Assert(t, sources == nil)
				}
			}
		})
	}
}

func TestNestedContainerUnselectedInvalidLiteralPoisonsWholeRoot(t *testing.T) {
	t.Parallel()
	for _, nested := range []string{
		"{ get value(): Value { return second; } }", "{ value: first, get other(): number { return 1; } }",
		"{ value: first, other(): Value { return second; } }", "{ value: first, ...{ other: second } }",
		"{ [\"value\"]: first }", "{ __proto__: first, value: first }", "[first, , second]", "[first, ...[second]]",
		"({ value: first } as { value: Value })",
	} {
		t.Run(nested, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+"const root = { branch: { value: first }, invalid: "+nested+", plain: first }; const alias = root; const probe = alias.plain; probe;")
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "validating only the selected path cannot certify the entire literal tree")
		})
	}
}

func TestNestedContainerUnsupportedOriginsAndContractsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"let root = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root: any = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root = { branch: { value: first } } as { branch: { value: Value } }; const probe = root.branch.value; probe;",
		"const root = { branch: { value: first } }; const probe = (root.branch as { value: Value }).value; probe;",
		"const inner = { value: first }; const root = { branch: inner }; const probe = root.branch.value; probe;",
		"function inner(): { value: Value } { return { value: first }; } const root = { branch: inner() }; const probe = root.branch.value; probe;",
		"const root = { branch: choose() ? { value: first } : { value: second } }; const probe = root.branch.value; probe;",
		"function selected(root: { branch: { value: Value } }): void { const probe = root.branch.value; probe; } selected({ branch: { value: first } });",
		"function selected(): { branch: { value: Value } } { return { branch: { value: first } }; } const probe = selected().branch.value; probe;",
		"const root: { branch: { value: any } } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root: { branch: { value: unknown } } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root: { branch: { value?: Value } } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root: { branch: Record<string, Value> } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root: { branch: { value(): void } } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"interface Inner { get value(): Value; } const root: { branch: Inner } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"interface Inner { (): void; value: Value; } // @ts-ignore: defensive source-only impossible contextual data contract\nconst root: { branch: Inner } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"interface Inner { new(): object; value: Value; } // @ts-ignore: defensive source-only impossible contextual data contract\nconst root: { branch: Inner } = { branch: { value: first } }; const probe = root.branch.value; probe;",
		"const root = { branch: [first, second] }; const alias: { branch: readonly Value[] } = root; const probe = root.branch[0]; probe;",
		"const root: { branch: [Value, Value] } = { branch: [first, second] }; const alias: { branch: Value[] } = root; const probe = root.branch[0]; probe;",
		"function outer<T>(value: T): void { const root = { branch: { value: first } }; const probe = root.branch.value; probe; } outer(1);",
		"const outer: <T>(value: T) => void = value => { const root = { branch: { value: first } }; const probe = root.branch.value; probe; }; outer(1);",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "this slice does not infer intermediate origins or contextual representation changes")
		})
	}
}

func TestNestedContainerFunctionAndReceiverCachesStaySeparate(t *testing.T) {
	t.Parallel()
	for _, receiverFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(receiverFirst), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} const root = { branch: [{ value: new A() }] }; const alias = root; const probe = alias.branch[0].value; probe;")
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

func TestNestedContainerBoundsNeverPublishPartialPositiveFacts(t *testing.T) {
	t.Parallel()
	const nestedDepth = 40
	for _, tt := range []struct {
		name  string
		body  string
		steps int
		depth int
	}{
		{"depth", "const root = " + strings.Repeat("{ branch: ", nestedDepth) + "{ value: first }" + strings.Repeat(" }", nestedDepth) + "; const probe = root" + strings.Repeat(".branch", nestedDepth) + ".value; probe;", maxValueFlowSteps, maxValueFlowDepth - nestedDepth},
		{"whole_tree_work", "const root = { branch: { value: first }, wide: [" + strings.Repeat("first,", maxValueFlowSteps+1) + "] }; const probe = root.branch.value; probe;", maxValueFlowSteps, 0},
		{"small_budget", "const root = { branch: { value: first }, side: { value: second }, plain: first }; const alias = root; const probe = alias.branch.value; probe;", 3, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+tt.body)
			steps := tt.steps
			assert.Assert(t, l.valueSources(probe, &steps, tt.depth) == nil)
			if tt.name == "depth" {
				assert.Assert(t, steps > 0, "the depth cap, not total work, stopped the nested chain")
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "cached exhaustion remains unknown, never a partial positive")
			assert.Equal(t, steps, maxValueFlowSteps)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func nestedContainerPrelude(receivers bool) string {
	prelude := literalProjectionPrelude(receivers)
	if receivers {
		return strings.Replace(prelude, "const first = new A(); const second = new B();", "const first: Value = new A(); const second: Value = new B();", 1)
	}
	prelude = strings.Replace(prelude, "function first(): void {}", "const first: Value = function first(): void {};", 1)
	return strings.Replace(prelude, "function second(): void {}", "const second: Value = function second(): void {};", 1)
}

func nestedContainerProbeDeclarations(path string) string {
	var source strings.Builder
	for _, name := range []string{"root", "alias", "other"} {
		fmt.Fprintf(&source, "\nconst check_%s = %s%s;\n", name, name, path)
	}
	source.WriteString("const check_plain = root.plain; const probe = check_root; probe;\n")
	return source.String()
}
