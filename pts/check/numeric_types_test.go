package check_test

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/check"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"gotest.tools/v3/assert"
)

// tscTypes loads source as /p/main.pts, which must have no tsc errors, and describes the
// type tsc gives each of its top-level variables, by name: a numeric type, an array of
// one ("u8[]"), or tsc's name for anything else.
func tscTypes(t *testing.T, source string) map[string]string {
	t.Helper()
	program, errors := frontend.Load(t.Context(), vfstest.FromMap(map[string]string{"/p/main.pts": source}, true /*useCaseSensitiveFileNames*/), "/p/main.pts")
	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	tc, release := program.TS.GetTypeCheckerForFile(t.Context(), program.Entry)
	defer release()
	types := map[string]string{}
	for _, statement := range program.Entry.Statements.Nodes {
		if statement.Kind != ast.KindVariableStatement {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			name := declaration.Name()
			typ := tc.GetTypeAtLocation(name)
			switch n, ok := check.NumericOf(typ); {
			case ok:
				types[name.Text()] = n.String()
			case tc.IsArrayType(typ):
				element, _ := check.NumericOf(tc.GetTypeArguments(typ)[0])
				types[name.Text()] = element.String() + "[]"
			default:
				types[name.Text()] = tc.TypeToString(typ)
			}
		}
	}
	return types
}

func TestPreludeNumericTypesAreTypesOfTheirOwnToTsc(t *testing.T) {
	t.Parallel()
	types := tscTypes(t, `type Byte = u8;
const byte: Byte = 1;
const bytes: u8[] = [1, 2];
const copy = bytes;
const withLiteral = [byte, 1]; // a literal without a context is a number
const long: i64 = 1;
const alsoLong: int = long;
const real: f64 = 1;
const sizes = new Map<string, u16>();
const size = sizes.get("a")!;
`)
	assert.DeepEqual(t, types, map[string]string{
		"byte": "u8", "bytes": "u8[]", "copy": "u8[]", "withLiteral": "number[]",
		"long": "int", "alsoLong": "int", "real": "number", "sizes": "Map<string, u16>", "size": "u16",
	})
}

func TestTscTypesArithmeticByTheNumericRules(t *testing.T) {
	t.Parallel()
	types := tscTypes(t, `const byte: u8 = 1;
const long: int = 2;
let counter: i16 = 3;
const product = byte * 2;
const fraction = byte * 1.5;
const widened = byte + long;
const negated = -byte;
const complement = ~byte;
const incremented = counter++;
const plain = 1 + 2;
const mixed = long + plain;
const doubled = [long].map(x => x * 2);
const total = [byte].reduce((a, b) => a + b);
`)
	assert.DeepEqual(t, types, map[string]string{
		"byte": "u8", "long": "int", "counter": "i16",
		"product": "u8", "fraction": "number", "widened": "int", "negated": "u8", "complement": "u8", "incremented": "i16",
		"plain": "number", "mixed": "number", "doubled": "int[]", "total": "u8",
	})
}
