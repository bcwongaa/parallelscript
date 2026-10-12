package lsp_test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
	"github.com/microsoft/typescript-go/internal/testutil/lsptestutil"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"github.com/microsoft/typescript-go/pts/lsp"
	"gotest.tools/v3/assert"
)

const dir = "/home/projects/app"

// startServer starts pts's language server on files, initialized and with each file open.
func startServer(t *testing.T, files map[string]string) *lsptestutil.LSPClient {
	t.Helper()
	if !bundled.Embedded {
		t.Skip("the TypeScript library files aren't embedded")
	}
	answer := func(_ context.Context, req *lsproto.RequestMessage) *lsproto.ResponseMessage {
		switch req.Method {
		case lsproto.MethodWorkspaceConfiguration:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: []any{nil}}
		case lsproto.MethodClientRegisterCapability, lsproto.MethodClientUnregisterCapability:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: lsproto.Null{}}
		}
		return nil
	}
	client, closeClient := lsptestutil.NewLSPClient(t, lsp.ServerOptions(nil, nil, io.Discard, dir, vfstest.FromMap(files, false)), answer)
	t.Cleanup(func() { _ = closeClient() })
	initialized, _, ok := lsptestutil.SendRequest(t, client, lsproto.InitializeInfo, &lsproto.InitializeParams{Capabilities: &lsproto.ClientCapabilities{}})
	assert.Assert(t, ok && initialized.AsResponse().Error == nil, "initialize failed")
	lsptestutil.SendNotification(t, client, lsproto.InitializedInfo, &lsproto.InitializedParams{})
	<-client.Server.InitComplete()
	for path, text := range files {
		lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidOpenInfo, &lsproto.DidOpenTextDocumentParams{
			TextDocument: &lsproto.TextDocumentItem{Uri: uriOf(path), LanguageId: "parallelscript", Text: text},
		})
	}
	return client
}

func uriOf(path string) lsproto.DocumentUri {
	return lsproto.DocumentUri("file://" + path)
}

// diagnosticsOf pulls a file's errors, each as "code line:col-line:col message", sorted
// (the editor's hints, such as unused variables, are left out).
func diagnosticsOf(t *testing.T, client *lsptestutil.LSPClient, path string) []string {
	t.Helper()
	_, report, ok := lsptestutil.SendRequest(t, client, lsproto.TextDocumentDiagnosticInfo, &lsproto.DocumentDiagnosticParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uriOf(path)},
	})
	assert.Assert(t, ok && report.FullDocumentDiagnosticReport != nil, "no diagnostic report for %s", path)
	var lines []string
	for _, d := range report.FullDocumentDiagnosticReport.Items {
		if d.Severity == nil || *d.Severity != lsproto.DiagnosticSeverityError {
			continue
		}
		code := ""
		switch {
		case d.Code != nil && d.Code.String != nil:
			code = *d.Code.String
		case d.Code != nil && d.Code.Integer != nil:
			code = fmt.Sprintf("TS%d", *d.Code.Integer)
		}
		r := d.Range
		lines = append(lines, fmt.Sprintf("%s %d:%d-%d:%d %s", code, r.Start.Line, r.Start.Character, r.End.Line, r.End.Character, *d.Message.String))
	}
	slices.Sort(lines)
	return lines
}

// A ParallelScript file gets no tsc errors for `parallel`, integer types and pts: modules,
// and gets ParallelScript's own errors, at the right places: the guardrail's (PTS3001)
// and a literal its integer type can't hold (PTS2002).
func TestPtsFilesGetParallelScriptDiagnostics(t *testing.T) {
	t.Parallel()
	main := dir + "/main.pts"
	client := startServer(t, map[string]string{
		main: `import { range } from "pts:parallel";

const small: u8 = 200;
const big: u8 = 300;
let total = 0;
parallel for (const i of range(0, 10)) {
  total += i;
}
console.log(small, big, total, await parallel Math.sqrt(16));
`,
	})
	got := diagnosticsOf(t, client, main)
	assert.Equal(t, len(got), 2, "%q", got)
	assert.Assert(t, strings.HasPrefix(got[0], "PTS2002 3:16-3:19 "), "%q", got)
	assert.Assert(t, strings.HasPrefix(got[1], "PTS3001 6:2-6:7 "), "%q", got)
}

// tsc's errors are reported as before, and ParallelScript's checks wait until there are none.
func TestTscErrorsComeFirst(t *testing.T) {
	t.Parallel()
	main := dir + "/broken.pts"
	client := startServer(t, map[string]string{
		main: `const n: number = "one";
const b: u8 = 300;
`,
	})
	got := diagnosticsOf(t, client, main)
	assert.Equal(t, len(got), 1, "%q", got)
	assert.Assert(t, strings.HasPrefix(got[0], "TS2322 0:6-0:7 "), "%q", got)
}

// Go to definition of a pts: module's export leads into the library, whose files have ptslib:/// URIs.
func TestDefinitionsReachTheLibrary(t *testing.T) {
	t.Parallel()
	main := dir + "/main.pts"
	client := startServer(t, map[string]string{main: "import { range } from \"pts:parallel\";\nconsole.log(range(0, 3).par.sum());\n"})
	_, result, ok := lsptestutil.SendRequest(t, client, lsproto.TextDocumentDefinitionInfo, &lsproto.DefinitionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uriOf(main)},
		Position:     lsproto.Position{Line: 1, Character: 13},
	})
	assert.Assert(t, ok)
	var uris []lsproto.DocumentUri
	if result.Locations != nil {
		for _, location := range *result.Locations {
			uris = append(uris, location.Uri)
		}
	}
	if result.Location != nil {
		uris = append(uris, result.Location.Uri)
	}
	assert.Assert(t, slices.Contains(uris, lsproto.DocumentUri("ptslib:///parallel.pts")), "%v", uris)
	assert.DeepEqual(t, diagnosticsOf(t, client, main), []string(nil))
}
