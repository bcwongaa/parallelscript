package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestExtractedContainerDemandsOnlyInstantiateSelectedTerminals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, origin := range []string{"object_child", "array_child", "array_object", "recursive_extraction", "root_and_child_alias", "fanout", "selected_return", "terminal_union"} {
				t.Run(origin, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, extractedContainerFlowSource(kind, origin))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					pairs := 0
					for value := range 3 {
						for demand := range 3 {
							required := value == demand || origin == "terminal_union" && value == 1 && demand == 0
							name := literalProjectionTarget(kind, value, demand)
							present := strings.Contains(c, name)
							if present {
								pairs++
							}
							assert.Check(t, present == required, "%s: present=%t, actual extracted source requires=%t", name, present, required)
							if kind != "function" {
								assert.Check(t, strings.Contains(c, "return "+name) == required, "%s registration must follow the required target", name)
							}
						}
					}
					want := 3
					if origin == "terminal_union" {
						want++
					}
					t.Logf("3 extracted sources x 3 signatures: %d target pairs; want %d", pairs, want)
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

func extractedContainerFlowSource(kind, origin string) string {
	var source strings.Builder
	source.WriteString(literalProjectionFlowPrelude())
	valueType := "Factory"
	if kind == "receiver" || kind == "captured_receiver" {
		valueType = "Maker"
	}
	for i := range 3 {
		fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
		fmt.Fprintf(&source, "function source%d<T>(value: T): Box<T> { return new Box(value); }\n", i)
		fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
		if kind == "receiver" {
			fmt.Fprintf(&source, "function use%d(receiver: Maker): void { const box: Box<C%d> = receiver.make(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		} else {
			fmt.Fprintf(&source, "function use%d(factory: Factory): void { const box: Box<C%d> = factory(new C%d()); console.log(box.value.tag); }\n", i, i, i)
		}
		if kind == "captured_receiver" {
			fmt.Fprintf(&source, "function read%d(receiver: Maker): Factory { return receiver.make; }\n", i)
		} else if kind == "wrapper" {
			fmt.Fprintf(&source, "function read%d(): Factory { const receiver: Maker = new M%d(); return receiver.make; }\n", i, i)
		}
	}
	for i := range 3 {
		value, other := fmt.Sprintf("source%d", i), fmt.Sprintf("source%d", (i+1)%3)
		if valueType == "Maker" {
			value, other = fmt.Sprintf("new M%d()", i), fmt.Sprintf("new M%d()", (i+1)%3)
		} else if kind == "wrapper" {
			value, other = fmt.Sprintf("read%d()", i), fmt.Sprintf("read%d()", (i+1)%3)
		}
		// Common declared terminal contracts make array-tree proofs reachable.
		fmt.Fprintf(&source, "const selected%d: %s = %s; const alternate%d: %s = %s;\n", i, valueType, value, i, valueType, other)
		selected, alternate := fmt.Sprintf("selected%d", i), fmt.Sprintf("alternate%d", i)
		if origin == "terminal_union" && i == 0 {
			selected = "choose() ? " + selected + " : " + alternate
		}
		expression := fmt.Sprintf("child%d.value", i)
		switch origin {
		case "array_child":
			fmt.Fprintf(&source, "const root%d = { child: [%s, %s] }; const child%d = root%d.child; const alias%d = child%d;\n", i, selected, alternate, i, i, i, i)
			expression = fmt.Sprintf("alias%d[0]", i)
		case "array_object":
			fmt.Fprintf(&source, "const root%d = [{ value: %s, other: %s }]; const child%d = root%d[0]; const alias%d = child%d;\n", i, selected, alternate, i, i, i, i)
			expression = fmt.Sprintf("alias%d.value", i)
		case "recursive_extraction":
			fmt.Fprintf(&source, "const root%d = { branch: { rows: [{ value: %s, other: %s }] } }; const branch%d = root%d.branch; const rows%d = branch%d.rows; const child%d = rows%d[0];\n", i, selected, alternate, i, i, i, i, i, i)
		case "root_and_child_alias":
			fmt.Fprintf(&source, "const root%d = { child: { value: %s, other: %s } }; const rootAlias%d = root%d; const rootOther%d = rootAlias%d; const child%d = rootOther%d.child; const alias%d = child%d; const other%d = alias%d;\n", i, selected, alternate, i, i, i, i, i, i, i, i, i, i)
			expression = fmt.Sprintf("other%d.value", i)
		case "fanout":
			fmt.Fprintf(&source, "const root%d = { child: { value: %s, other: %s }, side: [%s] }; const rootAlias%d = root%d; const child%d = root%d.child; const sibling%d = rootAlias%d.child; const side%d = root%d.side; const ignored%d = sibling%d.other; const ignoredSide%d = side%d[0];\n", i, selected, alternate, alternate, i, i, i, i, i, i, i, i, i, i, i, i)
		case "selected_return":
			fmt.Fprintf(&source, "function select%d(): %s { const root = { child: { value: %s, other: %s } }; const child = root.child; const alias = child; return alias.value; }\n", i, valueType, selected, alternate)
			expression = fmt.Sprintf("select%d()", i)
		default:
			fmt.Fprintf(&source, "const root%d = { child: { value: %s, other: %s } }; const child%d = root%d.child;\n", i, selected, alternate, i, i)
		}
		if kind == "captured_receiver" {
			expression = fmt.Sprintf("read%d(%s)", i, expression)
		}
		fmt.Fprintf(&source, "use%d(%s);\n", i, expression)
	}
	return source.String()
}

func TestExtractedContainerSharedSitesJoinEveryCaller(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var source strings.Builder
			source.WriteString(literalProjectionFlowPrelude())
			for i := range 3 {
				fmt.Fprintf(&source, "class C%d { tag = %d; }\n", i, i)
				fmt.Fprintf(&source, "function source%d<T>(value: T): Box<T> { return new Box(value); }\n", i)
				fmt.Fprintf(&source, "class M%d implements Maker { make<T>(value: T): Box<T> { return new Box(value); } }\n", i)
				if kind == "receiver" {
					fmt.Fprintf(&source, "function use%d(receiver: Maker): void { const box: Box<C%d> = receiver.make(new C%d()); console.log(box.value.tag); }\n", i, i, i)
				} else {
					fmt.Fprintf(&source, "function use%d(factory: Factory): void { const box: Box<C%d> = factory(new C%d()); console.log(box.value.tag); }\n", i, i, i)
				}
			}
			valueType, resultType, selected := "Factory", "Factory", "alias.value"
			if kind != "function" {
				valueType = "Maker"
				if kind == "receiver" {
					resultType = "Maker"
				} else {
					selected += ".make"
				}
			}
			for _, name := range []string{"shared", "separate"} {
				fmt.Fprintf(&source, "function %s(value: %s): %s { const root = { branch: [{ value }] }; const rows = root.branch; const child = rows[0]; const alias = child; return %s; }\n", name, valueType, resultType, selected)
			}
			for i := range 3 {
				name := "shared"
				if i == 2 {
					name = "separate"
				}
				value := fmt.Sprintf("source%d", i)
				if kind != "function" {
					value = fmt.Sprintf("new M%d()", i)
				}
				fmt.Fprintf(&source, "use%d(%s(%s));\n", i, name, value)
			}
			entry := writeSource(t, source.String())

			c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			for value := range 3 {
				for demand := range 3 {
					required := value < 2 && demand < 2 || value == 2 && demand == 2
					name := literalProjectionTarget(kind, value, demand)
					assert.Check(t, strings.Contains(c, name) == required, "%s: one extracted site summarizes every caller, not one runtime capture", name)
				}
			}
			if kind == "captured_receiver" {
				assertCapturedReceiverWrapperSignatures(t, c, "shared_make", []int{0, 1})
				assertCapturedReceiverWrapperSignatures(t, c, "separate_make", []int{2})
			}
		})
	}
}

func TestExtractedContainerUnsafeUsesKeepEveryCompatibleTarget(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"function", "receiver", "captured_receiver", "wrapper"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name string
				use  string
			}{
				{"unmodified", ""},
				{"extracted_write", "other.value = second;"},
				{"sibling_write", "sibling.value = second;"},
				{"root_plain_write", "alias.plain = second;"},
				{"captured_write", "function change(): void { other.value = second; } change();"},
				{"passed_child", "function replace(value: { value: Value; other: Value }): void { value.value = second; } replace(other);"},
				{"returned_child", "function expose(): { value: Value; other: Value } { return other; } expose().value = second;"},
				{"stored_child", "const stored = [other];"},
				{"dynamic_alias", "const dynamic: any = other;"},
				{"mutable_alias", "let mutable = other;"},
				{"asserted_alias", "const asserted = other as { value: Value; other: Value };"},
				{"dynamic_key", "let key: \"value\" = \"value\"; const ignored = other[key];"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					entry := writeSource(t, extractedContainerUnknownFlowSource(kind, tt.use))

					c, err := driver.EmitCWith(t.Context(), entry, lower.AllInstances)

					assert.NilError(t, err)
					for value := range 3 {
						for demand := range 3 {
							name := literalProjectionTarget(kind, value, demand)
							required := tt.use != "" || value == 0
							assert.Check(t, strings.Contains(c, name) == required, "%s: unmodified control prunes, any root/child/sibling poison retains every compatible target", name)
							if kind != "function" {
								assert.Check(t, strings.Contains(c, "return "+name) == required, "%s registration follows the target", name)
							}
						}
					}
					if kind == "captured_receiver" {
						for wrapper := range 3 {
							assertCapturedReceiverWrapperSignatures(t, c, fmt.Sprintf("read%d_make", wrapper), []int{wrapper})
						}
					}
				})
			}
		})
	}
}

func extractedContainerUnknownFlowSource(kind, unsafeUse string) string {
	// Reuse the exact target/call-site inventory of the D-210 unknown control.
	// Only the container component and selected terminals change here.
	source := nestedContainerUnknownFlowSource(kind, unsafeUse)
	source = strings.Replace(source,
		"const alias = root; const other = alias;\n",
		"const alias = root; const extracted = alias.branch; const other = extracted; const sibling = root.side;\n", 1)
	source = strings.ReplaceAll(source, "alias.side.value", "sibling.value")
	source = strings.ReplaceAll(source, "other.plain", "other.value")
	return source
}
