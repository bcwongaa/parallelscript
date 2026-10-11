package diagnostic_test

import (
	"testing"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"gotest.tools/v3/assert"
)

func TestFormatShowsFilesBelowTheDirectoryRelatively(t *testing.T) {
	t.Parallel()
	d := diagnostic.Diagnostic{File: "/p/src/main.pts", Line: 3, Column: 5, Code: "PTS1001", Message: "x is not supported yet"}

	assert.Equal(t, d.Format("/p"), "src/main.pts:3:5 - error PTS1001: x is not supported yet")
	assert.Equal(t, d.Format("/q"), "/p/src/main.pts:3:5 - error PTS1001: x is not supported yet")
}

func TestFormatOmitsTheLocationOfFilelessErrors(t *testing.T) {
	t.Parallel()
	d := diagnostic.Diagnostic{Code: "TS5023", Message: "Unknown compiler option."}

	assert.Equal(t, d.Format("/p"), "error TS5023: Unknown compiler option.")
}
