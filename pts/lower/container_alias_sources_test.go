package lower

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"gotest.tools/v3/assert"
)

func TestContainerAliasSourcesResolveOnlySelectedValues(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				body string
				want []string
			}{
				{"object_chain", "const root = { value: first, other: second }; const alias = root; const other = alias; const probe = other.value; probe;", []string{"first"}},
				{"nested_container_path", "const root = { inner: { value: first } }; const alias = root; const probe = alias.inner.value; probe;", []string{"first"}},
				{"array_chain", "const root = [first, second]; const alias = root; const other = alias; const probe = other[1]; probe;", []string{"second"}},
				{"root_projection", "const root = { value: first }; const alias = root; const other = alias; const probe = root.value; probe;", []string{"first"}},
				{"parenthesized", "const root = ({ value: first }); const alias = (root); const other = ((alias)); const probe = (other).value; probe;", []string{"first"}},
				{"fanout", "const root = { value: first, other: second }; const alias = root; const sibling = root; const other = alias; const ignored = sibling.other; const probe = other.value; probe;", []string{"first"}},
				{"typed_alias", "const root: { value: Value; other: Value } = { value: first, other: second }; const alias: { value: Value; other: Value } = root; const other = alias; const probe = other.value; probe;", []string{"first"}},
				{"readonly_alias", "const root: { value: Value } = { value: first }; const alias: Readonly<{ value: Value }> = root; const other = alias; const probe = other.value; probe;", []string{"first"}},
				{"shorthand", "const root = { first, second }; const alias = root; const other = alias; const probe = other.second; probe;", []string{"second"}},
				{"constant_string_key", "const root = { value: first }; const alias = root; const other = alias; const probe = other[\"value\"]; probe;", []string{"first"}},
				{"selected_alias", "const root = { value: first }; const alias = root; const other = alias; const selected = other.value; const probe = selected; probe;", []string{"first"}},
				{"selected_return", "function selected(): Value { return first; } const root = { value: selected() }; const alias = root; const other = alias; const probe = other.value; probe;", []string{"first"}},
				{"projected_return", "function selected(): Value { const root = { value: first }; const alias = root; const other = alias; return other.value; } const probe = selected(); probe;", []string{"first"}},
				{"selected_parameter", "function selected(value: Value): void { const root = { value }; const alias = root; const other = alias; const probe = other.value; probe; } selected(first); selected(second);", []string{"first", "second"}},
				{"conditional_selected_value", "const root = { value: choose() ? first : second }; const alias = root; const other = alias; const probe = other.value; probe;", []string{"first", "second"}},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					l, probe := valueFlowProbe(t, literalProjectionPrelude(receivers)+tt.body)
					steps := maxValueFlowSteps

					sources := (sourceProof{l: l, receivers: receivers}).valueSources(probe, &steps, 0)

					assert.Assert(t, sources != nil, "a completely checked immutable alias component proves its selected value")
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

func TestContainerAliasUnsafeUsesPoisonTheWholeComponent(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"root_write", "root.value = second;"},
				{"transitive_write", "other.value = second;"},
				{"sibling_write", "sibling.value = second;"},
				{"unselected_write", "other.other = second;"},
				{"captured_write", "function change(): void { other.value = second; }"},
				{"asserted_property_write", "(other as { value: Value }).value = second;"},
				{"asserted_target_write", "(other.value as Value) = second;"},
				{"pattern_write", "({ value: other.value } = { value: second });"},
				{"for_of_write", "for (other.value of [second]) {}"},
				{"delete", "delete (other as any).value;"},
				{"update", "other.count++;"},
				{"passed_container", "consume(other);"},
				{"returned_container", "function expose(): { value: Value } { return other; }"},
				{"stored_container", "const stored = [other];"},
				{"shorthand_escape", "const stored = { other };"},
				{"spread_escape", "const stored = { ...other };"},
				{"mutable_alias", "let mutable = other;"},
				{"any_alias", "const dynamic: any = other;"},
				{"unknown_alias", "const dynamic: unknown = other;"},
				{"any_property_alias", "const dynamic: { value: any } = other;"},
				{"unknown_property_alias", "const dynamic: { value: unknown } = other;"},
				{"asserted_alias", "const asserted = other as { value: Value };"},
				{"nonnull_alias", "const asserted = other!;"},
				{"satisfies_alias", "const asserted = other satisfies { value: Value };"},
				{"conditional_alias", "const merged = choose() ? other : sibling;"},
				{"logical_alias", "const merged = other || sibling;"},
				{"comma_alias", "const merged = (choose(), other);"},
				{"destructured_container", "const { value } = other;"},
				{"exported_alias", "export const outward = other;"},
				{"standalone_export", "export { other };"},
				{"type_query", "type Query = typeof other;"},
				{"dynamic_key", "let key: \"value\" = \"value\"; const ignored = other[key];"},
				{"asserted_read", "const ignored = (other as { value: Value }).value;"},
				{"missing_key", "// @ts-ignore: defensive missing-key source guard\nconst ignored = other[\"absent\"];"},
				{"implicit_this", "other.replace();"},
				{"parenthesized_implicit_this", "(other.replace)();"},
				{"optional_implicit_this", "other.replace?.();"},
				{"asserted_implicit_this", "(other.replace as typeof replace)();"},
				{"nonnull_implicit_this", "other.replace!();"},
				{"instantiated_implicit_this", "(other.replace<number>)(1);"},
				{"instantiated_optional_implicit_this", "((other.replace<number>))?.(1);"},
				{"tag_implicit_this", "other.replace``;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					for _, leafFirst := range []bool{false, true} {
						t.Run(fmt.Sprintf("leaf_first_%t", leafFirst), func(t *testing.T) {
							t.Parallel()
							// These are source-proof guards, not claims that native lowering
							// supports explicit-this, asserted assignment or tag syntax.
							source := literalProjectionPrelude(receivers) + `function replace<T>(this: { value: Value }, input?: T): void { this.value = second; }
const root = { value: first, other: first, count: 0, replace };
const alias = root; const other = alias; const sibling = root;
` + tt.use + containerAliasProbeDeclarations(".value")
							l, _ := valueFlowProbe(t, source)
							projections := containerAliasProbeExpressions(l)
							assert.Equal(t, len(projections), 4)
							if leafFirst {
								slices.Reverse(projections)
							}
							for _, projection := range projections {
								steps := maxValueFlowSteps
								assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0) == nil, "an unsafe use through any alias invalidates every component projection")
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

func TestContainerAliasArraysRejectUnsafeTransitiveUses(t *testing.T) {
	t.Parallel()
	for _, use := range []string{
		"other[0] = second;", "sibling.reverse();", "other.length = 0;", "const ignored = other.length;",
		"let index = 0; const ignored = other[index];", "consume(other);", "const stored = [...other];",
		"const opaque: unknown = other;", "const asserted = other as Value[];", "let mutable = other;",
	} {
		t.Run(use, func(t *testing.T) {
			t.Parallel()
			l, _ := valueFlowProbe(t, literalProjectionPrelude(false)+"const root = [first, second]; const alias = root; const other = alias; const sibling = root;\n"+use+containerAliasProbeDeclarations("[0]"))
			for _, projection := range containerAliasProbeExpressions(l) {
				steps := maxValueFlowSteps
				assert.Assert(t, l.valueSources(projection, &steps, 0) == nil)
			}
		})
	}
}

func TestContainerAliasUnsupportedOriginsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"let root = { value: first }; const alias = root; const probe = alias.value; probe;",
		"const root: any = { value: first }; const alias = root; const probe = alias.value; probe;",
		"const root = { value: first } as { value: Value }; const alias = root; const probe = alias.value; probe;",
		"export const root = { value: first }; const alias = root; const probe = alias.value; probe;",
		"const root = { value: first }; const alias = root; export { root }; const probe = alias.value; probe;",
		"function selected(root: { value: Value }): void { const alias = root; const probe = alias.value; probe; } selected({ value: first });",
		"function selected(): { value: Value } { return { value: first }; } const root = selected(); const alias = root; const probe = alias.value; probe;",
		"const root = { value: first, get other(): Value { return second; } }; const alias = root; const probe = alias.value; probe;",
		"const root = [first, , second]; const alias = root; const probe = alias[0]; probe;",
		"const root: { value: Value } = { value: first }; const alias: { value: any } = root; const probe = root.value; probe;",
		"const root: { value: Value } = { value: first }; const alias: { value: unknown } = root; const probe = root.value; probe;",
		"const root: { value: Value } = { value: first }; const alias: { value?: Value } = root; const probe = root.value; probe;",
		"const root: { value: Value } = { value: first }; const alias: { value(): void } = root; const probe = root.value; probe;",
		"const root: { value: Value } = { value: first }; const alias: { value: Value } | { value: Value; tag: number } = root; const probe = root.value; probe;",
		"const root: Value[] = [first]; const alias: readonly Value[] = root; const probe = root[0]; probe;",
		"const root: [Value, Value] = [first, second]; const alias: Value[] = root; const probe = root[0]; probe;",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
		})
	}
}

func TestContainerAliasFanoutIsWorkRatherThanAccumulatedDepth(t *testing.T) {
	t.Parallel()
	source := literalProjectionPrelude(false) + containerAliasFanout(maxValueFlowDepth+12) + "const probe = root.value; probe;"
	l, probe := valueFlowProbe(t, source)
	steps := maxValueFlowSteps

	sources := l.valueSources(probe, &steps, 0)

	assert.Assert(t, sources != nil, "many shallow aliases are not one deep alias chain")
	assert.DeepEqual(t, returnedSourceNames(sources), []string{"first"})
	assert.Assert(t, steps > 0)
}

func TestContainerAliasDepthWorkAndCachedUnknownGuards(t *testing.T) {
	t.Parallel()
	var deep strings.Builder
	deep.WriteString("const root = { value: first }; const alias0 = root;\n")
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
		{"reference_work", containerAliasFanout(maxValueFlowSteps+1) + "const probe = root.value; probe;", maxValueFlowSteps},
		{"transitive_work", "const root = { value: first }; const alias = root; const other = alias; " + strings.Repeat("other.value;", maxValueFlowSteps+1) + "const probe = root.value; probe;", maxValueFlowSteps},
		{"small_budget", "const root = { value: first }; const alias = root; const other = alias; const probe = other.value; probe;", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+tt.body)
			steps := tt.steps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			if tt.name == "depth" {
				assert.Assert(t, steps > 0, "the depth cap, not total work, stopped the alias chain")
			}
			steps = maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil, "cached exhaustion stays unknown, not an empty or partial positive set")
			assert.Equal(t, steps, maxValueFlowSteps)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestContainerAliasBudgetCannotPublishUncheckedSiblingFacts(t *testing.T) {
	t.Parallel()
	for _, receivers := range []bool{false, true} {
		t.Run(fmt.Sprintf("receivers_%t", receivers), func(t *testing.T) {
			t.Parallel()
			source := literalProjectionPrelude(receivers) + containerAliasFanout(maxValueFlowDepth+12) + `const alias = root; const other = alias; const sibling = root;
const escaped = sibling; consume(escaped);
const check_first = alias0.value;
` + containerAliasProbeDeclarations(".value")
			l, _ := valueFlowProbe(t, source)
			projections := containerAliasProbeExpressions(l)
			assert.Equal(t, len(projections), 5)
			steps := 12
			assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(projections[0], &steps, 0) == nil)
			for _, projection := range projections {
				steps = maxValueFlowSteps
				assert.Assert(t, (sourceProof{l: l, receivers: receivers}).valueSources(projection, &steps, 0) == nil, "exhaustion cannot publish early aliases as positive before checking an escaped sibling")
			}
			assert.Equal(t, len(l.valueFlow.active), 0)
			assert.Equal(t, len(l.valueFlow.receivers.active), 0)
		})
	}
}

func TestContainerAliasCyclesStayUnknown(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"// @ts-ignore: defensive source-only forward alias cycle\nconst root: { value: Value } = other; const alias: { value: Value } = root; const other: { value: Value } = alias; const probe = other.value; probe;",
		"function selected(): Value { return other.value; } const root = { value: selected() }; const alias = root; const other = alias; const probe = other.value; probe;",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, literalProjectionPrelude(false)+body)
			steps := maxValueFlowSteps
			assert.Assert(t, l.valueSources(probe, &steps, 0) == nil)
			assert.Assert(t, steps > 0)
			assert.Equal(t, len(l.valueFlow.active), 0)
		})
	}
}

func TestContainerAliasFunctionAndReceiverCachesStaySeparate(t *testing.T) {
	t.Parallel()
	for _, receiverFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(receiverFirst), func(t *testing.T) {
			t.Parallel()
			l, probe := valueFlowProbe(t, "class A {} const root = { value: new A() }; const alias = root; const other = alias; const probe = other.value; probe;")
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

func containerAliasProbeDeclarations(projection string) string {
	var source strings.Builder
	for _, name := range []string{"root", "alias", "other", "sibling"} {
		fmt.Fprintf(&source, "\nconst check_%s = %s%s;\n", name, name, projection)
	}
	source.WriteString("const probe = check_root; probe;\n")
	return source.String()
}

func containerAliasProbeExpressions(l *lowerer) []*ast.Node {
	var expressions []*ast.Node
	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		if node.Kind == ast.KindVariableDeclaration && node.Name().Kind == ast.KindIdentifier && strings.HasPrefix(node.Name().Text(), "check_") {
			expressions = append(expressions, node.Initializer())
		}
		node.ForEachChild(visit)
		return false
	}
	for _, file := range l.valueFlow.files {
		file.AsNode().ForEachChild(visit)
	}
	return expressions
}

func containerAliasFanout(count int) string {
	var source strings.Builder
	source.WriteString("const root = { value: first };\n")
	for i := range count {
		fmt.Fprintf(&source, "const alias%d = root; const selected%d = alias%d.value;\n", i, i, i)
	}
	return source.String()
}
