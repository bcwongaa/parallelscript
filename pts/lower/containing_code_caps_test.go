package lower_test

import (
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestContainingCodeCapsAValuePairingAbsentFromLaterFallbacks(t *testing.T) {
	t.Parallel()
	for _, owner := range []string{"plain_class", "generic_class"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			program, info := containingCodeProgram(t, containingCodeSource(t, owner == "generic_class"))

			_, diagnostics, narrowing := lower.ProgramWith(t.Context(), program, info, lower.AllInstances, nil)

			assert.Assert(t, len(diagnostics) > 0, "the direct AllInstances lowering must exercise the delayed class-body failure")
			assert.Assert(t, narrowing.Len() > 0, "the failure must have a removable speculative cause")
			assert.Check(t, narrowing.Within(nil, lower.ValueInstances), "ValueInstances never makes Bad.make<Dog[]> or its nested demand; the containing-code cap must prove this cut absent there")
			_, laterDiagnostics, _ := lower.ProgramWith(t.Context(), program, info, lower.ValueInstances, nil)
			assert.DeepEqual(t, laterDiagnostics, []diagnostic.Diagnostic(nil))
			_, narrowedDiagnostics, _ := lower.ProgramWith(t.Context(), program, info, lower.AllInstances, narrowing)
			assert.DeepEqual(t, narrowedDiagnostics, []diagnostic.Diagnostic(nil))
		})
	}
}

func TestContainingCodeCapsKeepSharedInstancesLaterFallbacksMake(t *testing.T) {
	t.Parallel()
	const valueRoot = `function valueRoot(): void {
  const via = <T,>(value: T): Box<T> => new Bad().make(value);
  const box: Box<Dog[]> = via([new Dog()]);
  console.log(box.value.length);
}
valueRoot();`
	for _, tt := range []struct {
		name    string
		root    string
		generic bool
	}{
		{"own_root", "const own = new Bad().make([new Dog()]); console.log(own.value.length);", false},
		{"generic_own_root", "const own = new Bad(0).make([new Dog()]); console.log(own.value.length);", true},
		{"value_root", valueRoot, false},
		{"generic_value_root", strings.ReplaceAll(valueRoot, "new Bad()", "new Bad(0)"), true},
	} {
		for _, placement := range []string{"before", "after"} {
			t.Run(tt.name+"_"+placement, func(t *testing.T) {
				t.Parallel()
				source := containingCodeSource(t, tt.generic)
				if placement == "before" {
					source = strings.Replace(source, "function main(): void {", tt.root+"\nfunction main(): void {", 1)
				} else {
					source += "\n" + tt.root + "\n"
				}
				program, info := containingCodeProgram(t, source)

				_, diagnostics, narrowing := lower.ProgramWith(t.Context(), program, info, lower.AllInstances, nil)

				assert.Assert(t, len(diagnostics) > 0, "the shared instance must still exercise the delayed failure")
				assert.Assert(t, narrowing.Len() > 0, "the failure must have a removable speculative cause")
				assert.Assert(t, !narrowing.Within(nil, lower.ValueInstances), "a shared Bad.make<Dog[]> also reached from %s must keep the later fallback check, regardless of user discovery order", tt.name)
			})
		}
	}
}

func containingCodeSource(t *testing.T, genericClass bool) string {
	t.Helper()
	data, err := os.ReadFile("../tests/run/generic_containing_value_pairing.pts")
	assert.NilError(t, err)
	source := string(data)
	if genericClass {
		source = strings.Replace(source, "class Bad implements Maker {", "class Bad<K> implements Maker {\n  constructor(public marker: K) {}", 1)
		source = strings.ReplaceAll(source, "new Bad()", "new Bad(0)")
	}
	return source
}

func containingCodeProgram(t *testing.T, source string) (*frontend.Program, *check.Info) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{"/p/main.pts": source}, true)
	program, diagnostics := frontend.Load(t.Context(), fs, "/p/main.pts")
	assert.DeepEqual(t, diagnostics, []diagnostic.Diagnostic(nil))
	info, diagnostics := check.Program(t.Context(), program)
	assert.DeepEqual(t, diagnostics, []diagnostic.Diagnostic(nil))
	return program, info
}
