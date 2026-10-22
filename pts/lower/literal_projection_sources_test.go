package lower

import (
	"fmt"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLiteralProjectionSourcesResolveOnlyTheSelectedDataValue(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"fresh_object", "const probe = ({ value: first, other: second }).value; probe;", []string{"first"}},
				{"fresh_array", "const probe = [first, second][1]; probe;", []string{"second"}},
				{"const_object", "const held = { value: first, other: second }; const probe = held.value; probe;", []string{"first"}},
				{"const_object_key", "const held = { value: first, other: second }; const probe = held[\"other\"]; probe;", []string{"second"}},
				{"const_array", "const held = [first, second]; const probe = held[0]; probe;", []string{"first"}},
				{"object_alias", "const held = { value: first }; const alias = held; const probe = held.value; probe;", []string{"first"}},
				{"shorthand", "const held = { first, second }; const probe = held.second; probe;", []string{"second"}},
				{"projected_value_aliases", "const held = { value: first }; const alias = held.value; const again = alias; const probe = again; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { return first; } const held = { value: selected() }; const probe = held.value; probe;", []string{"first"}},
				{"projected_return", "function selected(): Value { const held = { value: first }; return held.value; } const probe = selected(); probe;", []string{"first"}},
				{"selected_parameter", "function selected(value: Value): void { const held = { value }; const probe = held.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"multiple_projection_reads", "const held = { value: first, other: second }; const earlier = held.other; const probe = held.value; probe;", []string{"first"}},
				{"merged_selected_values", "const held = { value: choose() ? first : second }; const probe = held.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, literalProjectionPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps
					var sources functionSources
					if receivers {
						sources = l.receiverSources(probe, &steps, 0)
					} else {
						sources = l.valueSources(probe, &steps, 0)
					}
					assert.Assert(t, sources != nil, "a selected fresh data value has a proven source")
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
				})
			}
		})
	}
}

func TestLiteralProjectionUncertainContainersHaveUnknownSources(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
			}{
				{"mutable_object_binding", "let held = { value: first }; const probe = held.value; probe;"},
				{"mutable_array_binding", "let held = [first]; const probe = held[0]; probe;"},
				{"property_write", "const held = { value: first }; held.value = second; const probe = held.value; probe;"},
				{"keyed_property_write", "const held = { value: first }; held[\"value\"] = second; const probe = held.value; probe;"},
				{"element_write", "const held = [first]; held[0] = second; const probe = held[0]; probe;"},
				{"captured_property_write", "const held = { value: first }; function change(): void { held.value = second; } change(); const probe = held.value; probe;"},
				{"asserted_property_write", "const held = { value: first }; (held as { value: Value }).value = second; const probe = held.value; probe;"},
				{"pattern_property_write", "const held = { value: first }; ({ value: held.value } = { value: second }); const probe = held.value; probe;"},
				{"array_method", "const held = [first, second]; held.reverse(); const probe = held[0]; probe;"},
				{"array_length_write", "const held = [first]; held.length = 0; const probe = held[0]; probe;"},
				{"unrelated_write", "const held = { value: first, other: first }; held.other = second; const probe = held.value; probe;"},
				{"unrelated_dynamic_read", "const held = [first, second]; let index = 1; const other = held[index]; const probe = held[0]; probe;"},
				{"method_call_this_escape", "function replace(this: { value: Value }): void { this.value = second; } const held = { value: first, replace }; held.replace(); const probe = held.value; probe;"},
				{"parenthesized_method_call_this_escape", "function replace(this: { value: Value }): void { this.value = second; } const held = { value: first, replace }; (held.replace)(); const probe = held.value; probe;"},
				{"instantiated_method_call_this_escape", "function replace<T>(this: { value: Value }, value: T): void { this.value = second; } const held = { value: first, replace }; (held.replace<number>)(1); const probe = held.value; probe;"},
				{"instantiated_parenthesized_call_this_escape", "function replace<T>(this: { value: Value }, value: T): void { this.value = second; } const held = { value: first, replace }; ((held.replace<number>))(1); const probe = held.value; probe;"},
				{"instantiated_optional_call_this_escape", "function replace<T>(this: { value: Value }, value: T): void { this.value = second; } const held = { value: first, replace }; (held.replace<number>)?.(1); const probe = held.value; probe;"},
				{"optional_method_call_this_escape", "function replace(this: { value: Value }): void { this.value = second; } const held = { value: first, replace }; held.replace?.(); const probe = held.value; probe;"},
				{"tag_this_escape", "function replace(this: { value: Value }, strings: TemplateStringsArray): void { this.value = second; } const held = { value: first, replace }; held.replace``; const probe = held.value; probe;"},
				{"proto_member", "const held = { __proto__: first, value: first }; const probe = held.value; probe;"},
				{"alias_write", "const held = { value: first }; const alias = held; alias.value = second; const probe = held.value; probe;"},
				{"array_alias_write", "const held = [first]; const alias = held; alias[0] = second; const probe = held[0]; probe;"},
				{"passed_container", "const held = { value: first }; consume(held); const probe = held.value; probe;"},
				{"returned_container", "const held = { value: first }; function expose(): { value: Value } { return held; } const probe = held.value; probe;"},
				{"stored_container", "const held = { value: first }; const escaped = [held]; const probe = held.value; probe;"},
				{"shorthand_escape", "const held = { value: first }; const escaped = { held }; const probe = held.value; probe;"},
				{"exported_container", "export const held = { value: first }; const probe = held.value; probe;"},
				{"standalone_export", "const held = { value: first }; export { held }; const probe = held.value; probe;"},
				{"any_storage", "const held: any = { value: first }; const probe = held.value; probe;"},
				{"asserted_container", "const held = { value: first } as { value: Value }; const probe = held.value; probe;"},
				{"asserted_container_read", "const held = { value: first }; const probe = (held as { value: Value }).value; probe;"},
				{"readonly_escape", "const held: Readonly<{ value: Value }> = { value: first }; consume(held); const probe = held.value; probe;"},
				{"getter", "const held = { get value(): Value { return second; } }; const probe = held.value; probe;"},
				{"unselected_getter", "const held = { value: first, get other(): number { return 1; } }; const probe = held.value; probe;"},
				{"method_member", "const held = { value: first, other(): Value { return second; } }; const probe = held.value; probe;"},
				{"spread", "const extra = { other: second }; const held = { value: first, ...extra }; const probe = held.value; probe;"},
				{"computed_key", "const held = { [\"value\"]: first }; const probe = held.value; probe;"},
				{"duplicate_key", "// @ts-ignore: defensive source-only duplicate data key\nconst held = { value: first, value: second }; const probe = held.value; probe;"},
				{"array_hole", "const held = [first, , second]; const probe = held[0]; probe;"},
				{"array_spread", "const extra = [second]; const held = [first, ...extra]; const probe = held[0]; probe;"},
				{"dynamic_object_key", "const held = { value: first }; let key: \"value\" = \"value\"; const probe = held[key]; probe;"},
				{"dynamic_array_key", "const held = [first, second]; let key = 0; const probe = held[key]; probe;"},
				{"nested_container_path", "const held = { inner: { value: first } }; const probe = held.inner.value; probe;"},
				{"container_parameter", "function selected(held: { value: Value }): void { const probe = held.value; probe; } selected({ value: first });"},
				{"returned_container_path", "function selected(): { value: Value } { return { value: first }; } const probe = selected().value; probe;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, literalProjectionPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps
					var sources functionSources
					if receivers {
						sources = l.receiverSources(probe, &steps, 0)
					} else {
						sources = l.valueSources(probe, &steps, 0)
					}
					assert.Assert(t, sources == nil, "uncertain container sources are unknown, never empty")
				})
			}
		})
	}
}

func TestLiteralProjectionFunctionAndReceiverCachesStaySeparate(t *testing.T) {
	t.Parallel()
	for _, receiverFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(receiverFirst), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} const held = { value: new A() }; const probe = held.value; probe;")
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

func literalProjectionPrelude(receivers bool) string {
	if receivers {
		return `class A { tag = 0; }
class B { tag = 1; }
type Value = A;
const first = new A(); const second = new B();
function choose(): boolean { return true; }
function consume(value: unknown): void {}
`
	}
	return `type Value = () => void;
function first(): void {}
function second(): void {}
function choose(): boolean { return true; }
function consume(value: unknown): void {}
`
}
