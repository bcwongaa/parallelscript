package frontend_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

const strictConfig = `{ "compilerOptions": { "strict": true, "noEmit": true, "module": "esnext", "moduleResolution": "bundler", "allowImportingTsExtensions": true }, "files": ["main.ps"] }`

// newProgram builds a program from an in-memory project rooted at /p, configured by /p/tsconfig.json.
func newProgram(t *testing.T, files map[string]string) *compiler.Program {
	t.Helper()
	fs := bundled.WrapFS(vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/))
	host := compiler.NewCompilerHost("/p", fs, bundled.LibPath(), nil, nil)
	config, configErrors := tsoptions.GetParsedCommandLineOfConfigFile("/p/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	assert.Equal(t, len(configErrors), 0, "tsconfig errors: %v", configErrors)
	return compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
}

func allDiagnostics(t *testing.T, program *compiler.Program) []*ast.Diagnostic {
	t.Helper()
	ctx := t.Context()
	return slices.Concat(
		program.GetConfigFileParsingDiagnostics(),
		program.GetProgramDiagnostics(),
		program.GetSyntacticDiagnostics(ctx, nil),
		program.GetSemanticDiagnostics(ctx, nil),
		program.GetGlobalDiagnostics(ctx),
	)
}

func diagnosticCodes(diagnostics []*ast.Diagnostic) []int32 {
	codes := make([]int32, 0, len(diagnostics))
	for _, d := range diagnostics {
		codes = append(codes, d.Code())
	}
	return codes
}

func TestPsFileIsParsedAndTypeChecked(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": strictConfig,
		"/p/main.ps":       `const label: number = "not a number";`,
	})

	main := program.GetSourceFile("/p/main.ps")
	assert.Assert(t, main != nil)
	assert.Equal(t, main.ScriptKind, core.ScriptKindTS)
	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{2322})
}

func TestExtensionlessImportResolvesToPsFile(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": strictConfig,
		"/p/main.ps":       `import { answer } from "./util"; const n: number = answer; console.log(n);`,
		"/p/util.ps":       `export const answer: number = 42;`,
	})

	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{})
	assert.Assert(t, program.GetSourceFile("/p/util.ps") != nil)
}

func TestExtensionlessImportPrefersPsOverTs(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": strictConfig,
		"/p/main.ps":       `import { answer } from "./util"; const n: number = answer; console.log(n);`,
		"/p/util.ps":       `export const answer: number = 42;`,
		"/p/util.ts":       `export const answer: string = "the .ts twin must not win";`,
	})

	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{})
	assert.Assert(t, program.GetSourceFile("/p/util.ts") == nil)
}

func TestExtensionlessImportStillFindsTsFile(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": strictConfig,
		"/p/main.ps":       `import { answer } from "./util"; console.log(answer);`,
		"/p/util.ts":       `export const answer = 42;`,
	})

	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{})
	assert.Assert(t, program.GetSourceFile("/p/util.ts") != nil)
}

func TestExplicitPsImportResolves(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": strictConfig,
		"/p/main.ps":       `import { answer } from "./util.ps"; const n: number = answer; console.log(n);`,
		"/p/util.ps":       `export const answer: number = 42;`,
	})

	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{})
	assert.Assert(t, program.GetSourceFile("/p/util.ps") != nil)
}

func TestMissingPsImportIsReported(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": strictConfig,
		"/p/main.ps":       `import { answer } from "./nowhere"; console.log(answer);`,
	})

	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{2307})
}

func TestTsconfigIncludePicksUpPsFiles(t *testing.T) {
	t.Parallel()
	program := newProgram(t, map[string]string{
		"/p/tsconfig.json": `{ "compilerOptions": { "strict": true, "noEmit": true }, "include": ["src"] }`,
		"/p/src/a.ps":      `export const a = 1;`,
		"/p/src/b/c.ps":    `export const c = 2;`,
		"/p/src/notes.txt": `not a source file`,
	})

	var roots []string
	for _, file := range program.GetSourceFiles() {
		if strings.HasPrefix(file.FileName(), "/p/") {
			roots = append(roots, file.FileName())
		}
	}
	slices.Sort(roots)
	assert.DeepEqual(t, roots, []string{"/p/src/a.ps", "/p/src/b/c.ps"})
	assert.DeepEqual(t, diagnosticCodes(allDiagnostics(t, program)), []int32{})
}

func TestPsPathHelpers(t *testing.T) {
	t.Parallel()
	assert.Equal(t, core.GetScriptKindFromFileName("/p/main.ps"), core.ScriptKindTS)
	assert.Equal(t, tspath.ChangeExtension("/p/main.ps", ".js"), "/p/main.js")
	assert.Equal(t, tspath.RemoveFileExtension("/p/main.ps"), "/p/main")
	assert.Equal(t, tspath.TryGetExtensionFromPath("/p/main.ps"), tspath.ExtensionPs)
	assert.Assert(t, tspath.HasTSFileExtension("/p/main.ps"))
	assert.Assert(t, tspath.HasImplementationTSFileExtension("/p/main.ps"))
	assert.Equal(t, tspath.GetDeclarationEmitExtensionForPath("/p/main.ps"), tspath.ExtensionDts)
}
