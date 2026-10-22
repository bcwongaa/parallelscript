package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestArrayPatternContainerDemandsOnlyInstantiateSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"named", "root_alias", "projection", "fresh_projection", "extracted_root", "nested_array", "nested_object", "object_array", "multiple_leaves", "array_child", "selected_return", "terminal_union"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, arrayPatternContainerFlowSource(kind, origin, 3))
					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
					assert.NilError(t, err)
					pairs := inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
						return value == demand || origin == "terminal_union" && value == 1 && demand == 0
					})
					want := 3
					if origin == "terminal_union" {
						want++
					}
					t.Logf("direct AllInstances: 3 array-pattern sources x 3 demands = %d pairs; want %d", pairs, want)
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

func TestArrayPatternContainerUnsafeUsesKeepEveryCompatibleTarget(t *testing.T) {
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
				{"unselected_root_write", "root.unselected.value = second;"},
				{"array_assignment_target", "[other.value] = [second];"},
				{"escaped_sibling", "consumeContainer(sibling);"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"omission", "const [, rebound] = root.rows;"},
				{"default", "const [rebound = { value: first, other: first }] = root.rows;"},
				{"rest", "const [rebound, ...rest] = root.rows;"},
				{"terminal_sibling", "const [rebound, { value }] = root.rows;"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, arrayPatternContainerUnknownFlowSource(kind, tt.use))
					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
					assert.NilError(t, err)
					inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
						return tt.use != "" || value == 0
					})
				})
			}
		})
	}
}

func TestArrayPatternContainerSharedSitesJoinEveryCaller(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			inventoryKind := kind
			if kind == "captured_receiver" {
				inventoryKind = "function"
			}
			var source strings.Builder
			source.WriteString(inlineContainerFlowInventory(inventoryKind, 3))
			valueType, resultType, terminal := "Factory", "Factory", "child.value"
			if kind != "function" {
				valueType = "Maker"
				if kind == "receiver" {
					resultType = "Maker"
				} else {
					terminal += ".make"
				}
			}
			for _, name := range []string{"shared", "separate"} {
				fmt.Fprintf(&source, "function %s(value: %s): %s { const rows = [{ value }]; const [child] = rows; return %s; }\n", name, valueType, resultType, terminal)
			}
			for i := range 3 {
				name, value := "shared", fmt.Sprintf("source%d", i)
				if i == 2 {
					name = "separate"
				}
				if kind != "function" {
					value = fmt.Sprintf("new M%d()", i)
				}
				fmt.Fprintf(&source, "use%d(%s(%s));\n", i, name, value)
			}
			entry := writeSource(t, source.String())
			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)
			assert.NilError(t, err)
			inlineContainerAssertPairs(t, c, kind, 3, func(value, demand int) bool {
				return value < 2 && demand < 2 || value == 2 && demand == 2
			})
			if kind == "captured_receiver" {
				assertCapturedReceiverWrapperSignatures(t, c, "shared_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "separate_make", []int{2})
			}
		})
	}
}

func arrayPatternContainerFlowSource(kind, origin string, count int) string {
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
		case "root_alias":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }]; const alias%d = root%d; const [extracted%d] = ((alias%d)); const child%d = extracted%d;\n", i, selected, i, i, i, i, i, i)
		case "projection", "fresh_projection", "extracted_root", "object_array":
			fmt.Fprintf(&source, "const root%d = { rows: [{ value: %s }] };\n", i, selected)
			switch origin {
			case "fresh_projection":
				fmt.Fprintf(&source, "const [child%d] = ({ rows: [{ value: %s }] }).rows;\n", i, selected)
			case "extracted_root":
				fmt.Fprintf(&source, "const rows%d = root%d.rows; const [child%d] = rows%d;\n", i, i, i, i)
			case "object_array":
				fmt.Fprintf(&source, "const { rows: [child%d] } = root%d;\n", i, i)
			default:
				fmt.Fprintf(&source, "const [child%d] = root%d.rows;\n", i, i)
			}
		case "nested_array":
			fmt.Fprintf(&source, "const root%d = [[{ value: %s }]]; const [[child%d]] = root%d;\n", i, selected, i, i)
		case "nested_object":
			fmt.Fprintf(&source, "const root%d = [{ branch: { value: %s } }]; const [{ branch: child%d }] = root%d;\n", i, selected, i, i)
		case "multiple_leaves":
			fmt.Fprintf(&source, "const root%d = [{ value: %s }, { value: %s }]; const [child%d, sibling%d] = root%d; const ignored%d = sibling%d.value;\n", i, selected, other, i, i, i, i, i)
		case "array_child":
			fmt.Fprintf(&source, "const root%d = [[%s, %s]]; const [child%d] = root%d;\n", i, selected, other, i, i)
			terminal = fmt.Sprintf("child%d[0]", i)
		case "selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const rows = [{ value: %s }]; const [child] = rows; return child.value; }\n", i, valueType, selected)
			terminal = fmt.Sprintf("select%d()", i)
		default:
			fmt.Fprintf(&source, "const root%d = [{ value: %s }]; const [child%d] = root%d;\n", i, selected, i, i)
		}
		if origin == "escaped" {
			fmt.Fprintf(&source, "consumeContainer(child%d);\n", i)
		}
		if kind == "captured_receiver" {
			terminal = fmt.Sprintf("read%d(%s)", i, terminal)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, terminal)
	}
	return source.String()
}

func arrayPatternContainerUnknownFlowSource(kind, lateUse string) string {
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
	source.WriteString("const root = { rows: [{ value: first, other: first }, { value: first, other: first }], unselected: { value: first } }; const [extracted, sibling] = root.rows; const other = extracted;\n")
	for i, terminal := range []string{"root.rows[0].value", "sibling.value", "other.value"} {
		if kind == "captured_receiver" {
			terminal = fmt.Sprintf("read%d(%s)", i, terminal)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, terminal)
	}
	source.WriteString(lateUse + "\n")
	return source.String()
}
