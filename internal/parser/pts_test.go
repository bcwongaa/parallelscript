package parser_test

// PTS: tests of ParallelScript's contextual keyword `parallel` (D-O05, D-120).

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"gotest.tools/v3/assert"
)

func parsePts(t *testing.T, text string) *ast.SourceFile {
	t.Helper()
	file := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: "/main.pts", Path: "/main.pts"}, text, core.ScriptKindTS)
	assert.Equal(t, len(file.Diagnostics()), 0, "%q has syntax errors", text)
	return file
}

// parallelNodes lists the kinds of the nodes marked as `parallel`, with their text.
func parallelNodes(file *ast.SourceFile) []string {
	var found []string
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node.Flags&ast.NodeFlagsPtsParallel != 0 {
			found = append(found, node.Kind.String()+" "+file.Text()[node.Pos():node.End()])
		}
		return node.ForEachChild(visit)
	}
	file.AsNode().ForEachChild(visit)
	return found
}

func TestParallelOperatorIsACallMarkedParallel(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"const p = parallel f(x);":                 "KindCallExpression  parallel f(x)",
		"const r = await parallel build(user);":    "KindCallExpression  parallel build(user)",
		"urls.map((u) => parallel crawl(u));":      "KindCallExpression  parallel crawl(u)",
		"const p = parallel new Worker();":         "KindCallExpression  parallel new Worker()",
		"const p = parallel \"lit\";":              "KindCallExpression  parallel \"lit\"",
		"const p = parallel x;":                    "KindCallExpression  parallel x",
		"async function f() { parallel await x; }": "KindCallExpression  parallel await x",
		"const p = parallel async () => 1;":        "KindCallExpression  parallel async () => 1",
		"const p = parallel a.b.c(1) + 2;":         "KindCallExpression  parallel a.b.c(1)",
		"const p = parallel typeof x;":             "KindCallExpression  parallel typeof x",
		"const p = parallel { a: 1 };":             "KindCallExpression  parallel { a: 1 }",
	} {
		file := parsePts(t, text)
		got := parallelNodes(file)
		if want == "" {
			assert.Equal(t, len(got), 0, "%q", text)
			continue
		}
		assert.DeepEqual(t, got, []string{want})
	}
}

func TestParallelOperatorTakesAUnaryOperand(t *testing.T) {
	t.Parallel()
	file := parsePts(t, "const p = parallel f(x) + 1;")
	initializer := file.Statements.Nodes[0].AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0].Initializer()
	assert.Equal(t, initializer.Kind, ast.KindBinaryExpression)
	call := initializer.AsBinaryExpression().Left
	assert.Assert(t, call.Flags&ast.NodeFlagsPtsParallel != 0)
	assert.Equal(t, call.Expression().Text(), "parallel")
	assert.Equal(t, len(call.Arguments()), 1)
	assert.Equal(t, call.Arguments()[0].Kind, ast.KindCallExpression)
}

func TestParallelAsAnIdentifierStillParses(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"const parallel = 1; console.log(parallel + 1, parallel - 1, parallel * 2);",
		"parallel(x);",
		"parallel.run();",
		"parallel?.run();",
		"parallel[0];",
		"parallel`tag`;",
		"let parallel = 0; parallel = 2; parallel += 1; parallel++; parallel--;",
		"const o = { parallel }; const q = { parallel: 1, parallel2: parallel };",
		"const f = (parallel: number) => parallel;",
		"const g = parallel => parallel;",
		"const c = ok ? parallel : other;",
		"const d = parallel!;",
		"const e = parallel as number;",
		"const s = parallel satisfies number;",
		"for (parallel of items) {}",
		"if (x in parallel || parallel instanceof Y) {}",
		"parallel\nfoo();",
		"class C { parallel() {} parallel2 = 1; }",
		"function parallel(n: number) { return n; }",
		"parallel: for (;;) { break parallel; }",
		"const t: parallel = 1;",
	} {
		file := parsePts(t, text)
		assert.DeepEqual(t, parallelNodes(file), []string(nil))
	}
}

func TestParallelForIsTheForStatementMarkedParallel(t *testing.T) {
	t.Parallel()
	file := parsePts(t, "parallel for (const img of images) { resize(img); }\nparallel for (let i = 0; i < n; i++) work(i);")
	assert.DeepEqual(t, parallelNodes(file), []string{
		"KindForOfStatement parallel for (const img of images) { resize(img); }",
		"KindForStatement \nparallel for (let i = 0; i < n; i++) work(i);",
	})
}

func TestParallelThenForOnTheNextLineIsTwoStatements(t *testing.T) {
	t.Parallel()
	file := parsePts(t, "parallel\nfor (;;) {}")
	assert.Equal(t, len(file.Statements.Nodes), 2)
	assert.DeepEqual(t, parallelNodes(file), []string(nil))
}
