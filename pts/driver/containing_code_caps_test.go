package driver_test

import (
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/lower"
	"gotest.tools/v3/assert"
)

func TestContainingCodeAllowsANarrowerAllInstancesLowering(t *testing.T) {
	t.Parallel()
	for _, owner := range []string{"plain_class", "generic_class"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, containingCodeSource(t, owner == "generic_class"))

			c, err := driver.EmitCNarrowedWith(t.Context(), entry, lower.AllInstances)

			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(c, "pts_method_instance_find"), "leaving out a nested value pairing must retain the unrelated by-name call")
		})
	}
}

func TestContainingCodeKeepsTheDriversByNameInstances(t *testing.T) {
	t.Parallel()
	for _, owner := range []string{"plain_class", "generic_class"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			entry := writeSource(t, containingCodeSource(t, owner == "generic_class"))

			c, err := driver.EmitC(t.Context(), entry)

			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(c, "pts_method_instance_find"), "an AllInstances-only containing instance must not send the whole program to ValueInstances")
		})
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
