package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestElidedContainerDemandsRequireOnlySelectedLiteralTargets(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"leading", "interior", "trailing", "multiple", "root_alias", "projection", "fresh_projection", "extracted_root", "nested_array", "nested_object", "object_array", "array_child", "all_elided_use", "all_elided_sibling", "all_elided_terminal_sibling", "selected_return", "terminal_union"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, elidedContainerFlowSource(kind, origin, 3))
					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
					assert.NilError(t, err)
					pairs := inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
						return value == demand || origin == "terminal_union" && value == 1 && demand == 0
					})
					want := 3
					if origin == "terminal_union" {
						want++
					}
					assert.Check(t, pairs == want, "direct AllInstances: 3 elided-pattern sources x 3 demands = %d pairs; want %d", pairs, want)
					if kind == "captured_receiver" || kind == "wrapper" {
						for wrapper := range 3 {
							required := []int{wrapper}
							if kind == "wrapper" && origin == "terminal_union" && wrapper == 1 {
								required = []int{0, 1}
							}
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), required)
						}
					}
				})
			}
		})
	}
}

func TestElidedContainerUnsafeComponentsKeepEveryCompatiblePair(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"unmodified", ""},
				{"selected_write", "other.value = second;"},
				{"sibling_write", "sibling.other = second;"},
				{"elided_element_write", "root.rows[0].value = second;"},
				{"unbound_suffix_write", "root.rows[4].value = second;"},
				{"unselected_root_write", "root.unselected.value = second;"},
				{"array_replacement", "root.rows[2] = root.rows[1];"},
				{"array_assignment_target", "[other.value] = [second];"},
				{"escaped_sibling", "consumeContainer(sibling);"},
				{"escaped_elided_element", "consumeContainer(root.rows[0]);"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"default", "const [, rebound = { value: first, other: first }] = root.rows;"},
				{"rest", "const [, rebound, ...rest] = root.rows;"},
				{"terminal_sibling", "const [, rebound, , { value }] = root.rows;"},
				{"annotated_owner", "const [, rebound]: { value: Value; other: Value }[] = root.rows;"},
				{"elision_past_end", "const [, , , , , ,] = root.rows;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, elidedContainerUnknownFlowSource(kind, tt.use))
					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
					assert.NilError(t, err)
					pairs := inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
						return tt.use != "" || value == 0
					})
					want := 9
					if tt.use == "" {
						want = 3
					}
					assert.Check(t, pairs == want, "direct AllInstances component poison: %d required target pairs; want %d", pairs, want)
					if kind == "captured_receiver" {
						for wrapper := range 3 {
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{wrapper})
						}
					} else if kind == "wrapper" {
						assertCapturedReceiverWrapperSignatures(t, c, "read0_make", []int{0, 1, 2})
						if tt.use != "" {
							for _, wrapper := range []string{"read1_make", "read2_make"} {
								assertCapturedReceiverWrapperSignatures(t, c, wrapper, []int{0, 1, 2})
							}
						}
					}
				})
			}
		})
	}
}

func TestElidedContainerUnknownContractConversionRemainsUnsupported(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, elidedContainerUnknownFlowSource(kind, "const changed: { value: unknown; other: Value } = other;"))
			_, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
			assert.ErrorContains(t, err, "PTS1001")
			assert.ErrorContains(t, err, "whose `value` it doesn't provide with the same types")
		})
	}
}

func TestElidedContainerExcludedOriginsRetainCompatibleTargets(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"mutable_root", "mutable_pattern", "annotated_owner", "contextual_root", "any_root", "asserted_origin", "nonliteral_child", "parameter_container", "returned_container", "exhausted_alias_depth"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, elidedContainerFlowSource(kind, origin, 3))
					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
					assert.NilError(t, err)
					pairs := inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool { return true })
					assert.Equal(t, pairs, 9, "uncertain or exhausted elided patterns keep every compatible source, never a guessed diagonal")
					if kind == "captured_receiver" {
						for wrapper := range 3 {
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{wrapper})
						}
					} else if kind == "wrapper" {
						for wrapper := range 3 {
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{0, 1, 2})
						}
					}
				})
			}
		})
	}
}

func TestElidedContainerSharedSitesJoinAllTerminalCallers(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			inventoryKind := kind
			if kind == "captured_receiver" {
				inventoryKind = "function"
			}
			var source strings.Builder
			source.WriteString(inlineContainerFlowInventory(inventoryKind, 3))
			valueType, resultType, terminal := "Factory", "Factory", "child.value"
			if kind == "receiver" || kind == "captured_receiver" {
				valueType = "Maker"
				if kind == "receiver" {
					resultType = "Maker"
				} else {
					terminal += ".make"
				}
			}
			for _, name := range []string{"shared", "separate"} {
				fmt.Fprintf(&source, "function %s(value: %s): %s { const rows = [{ value }, { value }]; const [, child] = rows; const [, ,] = rows; return %s; }\n", name, valueType, resultType, terminal)
			}
			for i := range 3 {
				name, value := "shared", fmt.Sprintf("source%d", i)
				if i == 2 {
					name = "separate"
				}
				if valueType == "Maker" {
					value = fmt.Sprintf("new M%d()", i)
				} else if kind == "wrapper" {
					value = fmt.Sprintf("read%d()", i)
				}
				fmt.Fprintf(&source, "use%d(%s(%s));\n", i, name, value)
			}
			entry := writeSource(t, source.String())
			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
			assert.NilError(t, err)
			pairs := inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
				return value < 2 && demand < 2 || value == 2 && demand == 2
			})
			assert.Equal(t, pairs, 5, "one AST creation site represents both closed callers, while an independent site stays separate")
			if kind == "captured_receiver" {
				assertCapturedReceiverWrapperSignatures(t, c, "shared_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "separate_make", []int{2})
			} else if kind == "wrapper" {
				assertCapturedReceiverWrapperSignatures(t, c, "read0_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "read1_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "read2_make", []int{2})
			}
		})
	}
}

func elidedContainerFlowSource(kind, origin string, count int) string {
	var source strings.Builder
	source.WriteString(inlineContainerFlowInventory(kind, count))
	valueType := "Factory"
	if kind == "receiver" || kind == "captured_receiver" {
		valueType = "Maker"
	}
	for i := range count {
		value, alternate := fmt.Sprintf("source%d", i), fmt.Sprintf("source%d", (i+1)%count)
		if valueType == "Maker" {
			value, alternate = fmt.Sprintf("new M%d()", i), fmt.Sprintf("new M%d()", (i+1)%count)
		} else if kind == "wrapper" {
			value, alternate = fmt.Sprintf("read%d()", i), fmt.Sprintf("read%d()", (i+1)%count)
		}
		fmt.Fprintf(&source, "const selected%d: %s = %s; const alternate%d: %s = %s;\n", i, valueType, value, i, valueType, alternate)
		selected, other := fmt.Sprintf("selected%d", i), fmt.Sprintf("alternate%d", i)
		if origin == "terminal_union" && i == 0 {
			selected = "choose() ? " + selected + " : " + other
		}
		terminal := fmt.Sprintf("child%d.value", i)
		switch origin {
		case "leading", "terminal_union", "all_elided_use":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }, { value: %s }]; const [, child%d] = root%d;\n", i, other, selected, i, i)
			if origin == "all_elided_use" {
				fmt.Fprintf(&source, "const [, ,] = root%d;\n", i)
			}
		case "interior":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }, { value: %s }, { value: %s }]; const [left%d, , child%d] = root%d; const ignored%d = left%d.value;\n", i, other, other, selected, i, i, i, i, i)
		case "trailing":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }, { value: %s }]; const [child%d, ,] = root%d;\n", i, selected, other, i, i)
		case "multiple":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }, { value: %s }, { value: %s }, { value: %s }]; const [, , child%d, ,] = root%d;\n", i, other, other, selected, other, i, i)
		case "root_alias":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }, { value: %s }]; const rootAlias%d = root%d; const [, extracted%d] = ((rootAlias%d)); const child%d = extracted%d;\n", i, other, selected, i, i, i, i, i, i)
		case "projection", "extracted_root", "object_array":
			fmt.Fprintf(&source, "const root%d = { rows: [{ value: %s }, { value: %s }] };\n", i, other, selected)
			switch origin {
			case "extracted_root":
				fmt.Fprintf(&source, "const rows%d = root%d.rows; const [, child%d] = rows%d;\n", i, i, i, i)
			case "object_array":
				fmt.Fprintf(&source, "const { rows: [, child%d] } = root%d;\n", i, i)
			default:
				fmt.Fprintf(&source, "const [, child%d] = root%d.rows;\n", i, i)
			}
		case "fresh_projection":
			fmt.Fprintf(&source, "const [, child%d] = ({ rows: [{ value: %s }, { value: %s }] }).rows;\n", i, other, selected)
		case "nested_array":
			fmt.Fprintf(&source, "const root%d = [[{ value: %s }, { value: %s }], [{ value: %s }, { value: %s }]]; const [, [, child%d]] = root%d;\n", i, other, other, other, selected, i, i)
		case "nested_object":
			fmt.Fprintf(&source, "const root%d = [{ rows: [{ value: %s }, { value: %s }] }, { rows: [{ value: %s }, { value: %s }] }]; const [, { rows: [, child%d] }] = root%d;\n", i, other, other, other, selected, i, i)
		case "array_child":
			fmt.Fprintf(&source, "const root%d = [[%s, %s], [%s, %s]]; const [, child%d] = root%d;\n", i, other, other, other, selected, i, i)
			terminal = fmt.Sprintf("child%d[1]", i)
		case "all_elided_sibling":
			fmt.Fprintf(&source, "const root%d = { rows: [{ value: %s }, { value: %s }], skipped: [1, 2] }; const { rows: [, child%d], skipped: [, ,] } = root%d;\n", i, other, selected, i, i)
		case "all_elided_terminal_sibling":
			fmt.Fprintf(&source, "const root%d = { child: { value: %s }, skipped: [%s, %s] }; const { child: child%d, skipped: [, ,] } = root%d;\n", i, selected, other, other, i, i)
		case "selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const rows = [{ value: %s }, { value: %s }]; const [, child] = rows; return child.value; }\n", i, valueType, other, selected)
			terminal = fmt.Sprintf("select%d()", i)
		case "mutable_root", "mutable_pattern", "annotated_owner", "contextual_root", "any_root", "asserted_origin", "exhausted_alias_depth":
			rootKind, rootType, patternKind, patternType := "const", "", "const", ""
			if origin == "mutable_root" {
				rootKind = "let"
			} else if origin == "mutable_pattern" {
				patternKind = "let"
			} else if origin == "annotated_owner" {
				patternType = ": { value: " + valueType + " }[]"
			} else if origin == "contextual_root" {
				rootType = ": { value: " + valueType + " }[]"
			} else if origin == "any_root" {
				rootType = ": any"
			}
			fmt.Fprintf(&source, "%s root%d%s = [{ value: %s }, { value: %s }];\n", rootKind, i, rootType, other, selected)
			initializer := fmt.Sprintf("root%d", i)
			if origin == "asserted_origin" {
				initializer = "(" + initializer + " as { value: " + valueType + " }[])"
			}
			fmt.Fprintf(&source, "%s [, child%d]%s = %s;\n", patternKind, i, patternType, initializer)
			if origin == "exhausted_alias_depth" {
				const aliasDepth = 140
				fmt.Fprintf(&source, "const alias%d_0 = child%d;\n", i, i)
				for depth := 1; depth <= aliasDepth; depth++ {
					fmt.Fprintf(&source, "const alias%d_%d = alias%d_%d;\n", i, depth, i, depth-1)
				}
				terminal = fmt.Sprintf("alias%d_%d.value", i, aliasDepth)
			}
		case "nonliteral_child":
			fmt.Fprintf(&source, "const inner%d = { value: %s }; const root%d = [inner%d, inner%d]; const [, child%d] = root%d;\n", i, selected, i, i, i, i, i)
		case "parameter_container":
			fmt.Fprintf(&source, "function select%d(rows: { value: %s }[]): %s { const [, child] = rows; return child.value; }\n", i, valueType, valueType)
			terminal = fmt.Sprintf("select%d([{ value: %s }, { value: %s }])", i, other, selected)
		case "returned_container":
			fmt.Fprintf(&source, "function rows%d(): { value: %s }[] { return [{ value: %s }, { value: %s }]; } const [, child%d] = rows%d();\n", i, valueType, other, selected, i, i)
		default:
			panic("unsupported elided container origin: " + origin)
		}
		if kind == "captured_receiver" {
			terminal = fmt.Sprintf("read%d(%s)", i, terminal)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, terminal)
	}
	return source.String()
}

func elidedContainerUnknownFlowSource(kind, lateUse string) string {
	var source strings.Builder
	source.WriteString(inlineContainerFlowInventory(kind, 3))
	switch kind {
	case "receiver", "captured_receiver":
		source.WriteString("type Value = Maker; const first: Value = new M0(); const second: Value = new M1();\n")
	case "wrapper":
		source.WriteString("type Value = Factory; const first: Value = read0(); const second: Value = read1(); const keep: any[] = [first, second, read2()];\n")
	default:
		source.WriteString("type Value = Factory; const first: Value = source0; const second: Value = source1; const keep: any[] = [source0, source1, source2];\n")
	}
	source.WriteString("const root = { rows: [{ value: first, other: first }, { value: first, other: first }, { value: first, other: first }, { value: first, other: first }, { value: first, other: first }], unselected: { value: first }, skipped: [1, 2] }; const [, extracted, , sibling] = root.rows; const other = extracted; const { skipped: [, ,] } = root;\n")
	for i, terminal := range []string{"root.rows[0].value", "sibling.value", "other.value"} {
		if kind == "captured_receiver" {
			terminal = fmt.Sprintf("read%d(%s)", i, terminal)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, terminal)
	}
	source.WriteString(lateUse + "\n")
	return source.String()
}
