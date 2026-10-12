package frontend_test

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/frontend"
	"gotest.tools/v3/assert"
)

// load loads /p/main.pts from an in-memory project.
func load(t *testing.T, files map[string]string) (*frontend.Program, []diagnostic.Diagnostic) {
	t.Helper()
	return frontend.Load(t.Context(), vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/), "/p/main.pts")
}

func TestHelloWorldLoadsWithoutErrors(t *testing.T) {
	t.Parallel()
	program, errors := load(t, map[string]string{"/p/main.pts": `console.log("Hello, ParallelScript");`})

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	assert.Equal(t, program.Entry.FileName(), "/p/main.pts")
}

func TestTypeErrorIsReportedWithPosition(t *testing.T) {
	t.Parallel()
	_, errors := load(t, map[string]string{"/p/main.pts": "console.log(1);\nconst label: number = \"x\";\n"})

	assert.Equal(t, len(errors), 1)
	assert.Equal(t, errors[0].Format("/p"), `main.pts:2:7 - error TS2322: Type 'string' is not assignable to type 'number'.`)
}

func TestSyntaxErrorHidesSemanticErrors(t *testing.T) {
	t.Parallel()
	_, errors := load(t, map[string]string{"/p/main.pts": "const label: number = \"x\";\nconsole.log(;\n"})

	assert.Equal(t, len(errors), 1)
	assert.Equal(t, errors[0].Line, 2)
}

func TestMissingEntryFileIsReported(t *testing.T) {
	t.Parallel()
	_, errors := load(t, map[string]string{"/p/other.pts": ``})

	assert.Equal(t, len(errors), 1)
	assert.Equal(t, errors[0].Code, "TS6053")
}

func TestDomGlobalsAreNotDeclared(t *testing.T) {
	t.Parallel()
	_, errors := load(t, map[string]string{"/p/main.pts": `console.log(document.title);`})

	assert.Equal(t, len(errors), 1)
	assert.Assert(t, errors[0].Code == "TS2304" || errors[0].Code == "TS2584", errors[0].Format("/p"))
}

func TestFileWithoutImportsIsAModuleSoItCanShadowGlobals(t *testing.T) {
	t.Parallel()
	_, errors := load(t, map[string]string{"/p/main.pts": `const console = 1;`})

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
}

func TestModulesAreOrderedAsTheyRun(t *testing.T) {
	t.Parallel()
	program, errors := load(t, map[string]string{
		"/p/main.pts": `import "./a";
import type { T } from "./types";
import { d } from "./declarations";
import { b } from "./b.pts";
import "./a.pts";
export * from "./c";
`,
		"/p/a.pts":             `import "./b"; console.log("a");`,
		"/p/b.pts":             `import "./main"; export const b = 1;`,
		"/p/c.pts":             `export const c = 1;`,
		"/p/types.pts":         `export type T = number;`,
		"/p/declarations.d.ts": `export declare const d: number;`,
	})

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	// main.pts is already running when b.pts imports it; types.pts is imported only as
	// types; declarations.d.ts has no code.
	assert.DeepEqual(t, moduleNames(program), []string{"/p/b.pts", "/p/a.pts", "/p/c.pts", "/p/main.pts"})
}

func moduleNames(program *frontend.Program) []string {
	var names []string
	for _, module := range program.Modules {
		names = append(names, module.FileName())
	}
	return names
}

func TestPtsModulesAreTheLibrarysSources(t *testing.T) {
	t.Parallel()
	program, errors := load(t, map[string]string{"/p/main.pts": `import { join } from "pts:path";
console.log(join("a", "b"));
`})

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	// pts:path uses the global process, so pts:process runs first.
	assert.DeepEqual(t, moduleNames(program), []string{"ptslib:///process.pts", "ptslib:///path.pts", "/p/main.pts"})
}

func TestGlobalProcessRunsItsModuleFirst(t *testing.T) {
	t.Parallel()
	program, errors := load(t, map[string]string{
		"/p/main.pts":   `import { f } from "./other"; console.log(f());`,
		"/p/other.pts":  `export function f(): string[] { return process.argv; }`,
		"/p/unused.pts": `console.log(process.pid);`,
	})

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	assert.DeepEqual(t, moduleNames(program), []string{"ptslib:///process.pts", "/p/other.pts", "/p/main.pts"})
}

func TestShadowedProcessIsNotTheGlobal(t *testing.T) {
	t.Parallel()
	program, errors := load(t, map[string]string{"/p/main.pts": `function process(n: number): number { return n; }
console.log(process(1));
`})

	assert.DeepEqual(t, errors, []diagnostic.Diagnostic(nil))
	assert.DeepEqual(t, moduleNames(program), []string{"/p/main.pts"})
}
