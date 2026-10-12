package lsp

// PTS: library files that exist only inside the server's file system (ParallelScript's
// ptslib:/// files, TypeScript's embedded bundled:/// ones) can't be opened by a client on
// its own, so a definition there leads nowhere. LSP 3.18's workspace/textDocumentContent
// lets the client ask the server for such a document's text (D-169).

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
)

// ContentSchemes are the URI schemes whose documents the server reads out of its file
// system for workspace/textDocumentContent. Upstream's own server serves none.
var ContentSchemes []string

func textDocumentContentOptions() *lsproto.TextDocumentContentOptionsOrRegistrationOptions {
	if len(ContentSchemes) == 0 {
		return nil
	}
	return &lsproto.TextDocumentContentOptionsOrRegistrationOptions{
		Options: &lsproto.TextDocumentContentOptions{Schemes: ContentSchemes},
	}
}

func (s *Server) handleTextDocumentContent(_ context.Context, params *lsproto.TextDocumentContentParams, _ *lsproto.RequestMessage) (lsproto.TextDocumentContentResponse, error) {
	scheme, path, _ := strings.Cut(string(params.Uri), ":")
	if !slices.Contains(ContentSchemes, scheme) {
		return nil, fmt.Errorf("%w: no content for %s", lsproto.ErrorCodeInvalidParams, params.Uri)
	}
	// The files' names are URIs with an empty authority (ptslib:///parallel.pts), which
	// clients may write without it (VS Code asks for ptslib:/parallel.pts).
	text, ok := s.fs.ReadFile(scheme + ":///" + strings.TrimLeft(path, "/"))
	if !ok {
		return nil, fmt.Errorf("%w: %s doesn't exist", lsproto.ErrorCodeInvalidParams, params.Uri)
	}
	return &lsproto.TextDocumentContentResult{Text: text}, nil
}
