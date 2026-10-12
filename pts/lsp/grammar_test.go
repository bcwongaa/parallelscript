package lsp_test

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/scanner"
	"gotest.tools/v3/assert"
)

// samplesPath holds programs and, for each `parallel` in one, whether it is the keyword:
// the VS Code grammar's test checks its highlighting against them (D-169).
const samplesPath = "../editors/vscode/test/parallel_samples.json"

type parallelSample struct {
	Program  string `json:"program"`
	Keywords []bool `json:"keywords"`
}

// The editor's grammar highlights `parallel` as the keyword where the parser reads the
// keyword, in every sample its test checks.
func TestGrammarSamplesAgreeWithTheParser(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(samplesPath)
	assert.NilError(t, err)
	var file struct {
		Samples []parallelSample `json:"samples"`
	}
	assert.NilError(t, json.Unmarshal(data, &file))
	assert.Assert(t, len(file.Samples) > 0)
	word := regexp.MustCompile(`\bparallel\b`)
	for _, sample := range file.Samples {
		source := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: "/main.pts", Path: "/main.pts"}, sample.Program, core.ScriptKindTS)
		assert.Equal(t, len(source.Diagnostics()), 0, "%q has syntax errors", sample.Program)
		keywordAt := parallelKeywordOffsets(source)
		var got []bool
		for _, match := range word.FindAllStringIndex(sample.Program, -1) {
			got = append(got, keywordAt[match[0]])
		}
		assert.DeepEqual(t, got, sample.Keywords)
	}
}

// parallelKeywordOffsets are the offsets of the keywords the parser read: where each call
// or for statement marked as `parallel` starts.
func parallelKeywordOffsets(file *ast.SourceFile) map[int]bool {
	offsets := map[int]bool{}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node.Flags&ast.NodeFlagsPtsParallel != 0 {
			offsets[scanner.SkipTrivia(file.Text(), node.Pos())] = true
		}
		return node.ForEachChild(visit)
	}
	file.AsNode().ForEachChild(visit)
	return offsets
}
