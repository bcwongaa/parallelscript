package driver_test

// C176 (H09b5b): a failure only some speculative instances make costs other values none of
// theirs, and `pts emit-c` prints the C that the build compiles.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

// rejectingCompiler writes a C compiler for PTS_CC that rejects, as clang rejects C it can't
// compile, a program's C holding marker, compiles anything else with clang, and copies the
// last program C it compiled to the returned path.
func rejectingCompiler(t *testing.T, marker string) (cc string, compiled string) {
	t.Helper()
	dir := t.TempDir()
	cc, compiled = filepath.Join(dir, "cc"), filepath.Join(dir, "compiled.c")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
  */main.c)
    if grep -q '` + marker + `' "$arg"; then
      echo "$arg:1:1: error: rejected by the test's C compiler" >&2
      exit 1
    fi
    cp "$arg" '` + compiled + `'
    ;;
  esac
done
exec clang "$@"
`
	assert.NilError(t, os.WriteFile(cc, []byte(script), 0o755))
	return cc, compiled
}

func TestEmitCPrintsTheCThatBuildCompiles(t *testing.T) {
	// An interface call of a generic method looks its instance up by name only with
	// AllInstances (D-191). Where clang rejects AllInstances' C, Build falls back to
	// ValueInstances, which compiles; emit-c, which ran no C compiler, printed the
	// AllInstances C that no build compiles (C176). It checks its C as the build does.
	cc, compiled := rejectingCompiler(t, "pts_method_instance_find")
	t.Setenv("PTS_CC", cc)
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
interface Maker {
  make<T>(x: T): Box<T>;
}
class ClassMaker implements Maker {
  make<T>(x: T): Box<T> {
    return new Box(x);
  }
}
const m: Maker = new ClassMaker();
try {
  console.log(m.make(4).value + 1);
} catch (error: any) {
  console.log(error.message);
}
`)

	c, err := driver.EmitC(t.Context(), entry)
	assert.NilError(t, err)
	assert.NilError(t, driver.Build(t.Context(), entry, filepath.Join(t.TempDir(), "main")))

	built, err := os.ReadFile(compiled)
	assert.NilError(t, err)
	assert.Assert(t, c == string(built), "emit-c printed C that the build didn't compile (AllInstances' lookup in it: %v; in the built C: %v)",
		strings.Contains(c, "pts_method_instance_find"), strings.Contains(string(built), "pts_method_instance_find"))
}

// unlinkingCompiler writes a C compiler for PTS_CC that fails to link, as the linker fails on
// a function the C calls and nothing defines, a program's C holding marker, and checks and
// compiles anything else with clang.
func unlinkingCompiler(t *testing.T, marker string) string {
	t.Helper()
	cc := filepath.Join(t.TempDir(), "cc")
	script := `#!/bin/sh
link=yes
for arg in "$@"; do
  case "$arg" in
  -fsyntax-only) link=no ;;
  esac
done
for arg in "$@"; do
  case "$arg" in
  */main.c)
    if [ $link = yes ] && grep -q '` + marker + `' "$arg"; then
      printf "/usr/bin/ld: /tmp/main.o: in function \\140main':\\n" >&2
      printf "main.c:(.text+0x1): undefined reference to \\140pts_fn_missing'\\n" >&2
      exit 1
    fi
    ;;
  esac
done
exec clang "$@"
`
	assert.NilError(t, os.WriteFile(cc, []byte(script), 0o755))
	return cc
}

func TestABuildWhoseCDoesntLinkDoesntFallBack(t *testing.T) {
	// emit-c checks its C with the C compiler but doesn't link it, so a build whose C doesn't
	// link reports the compiler's bug (D-179) rather than building the next fallback's C,
	// which emit-c wouldn't print (D-199).
	t.Setenv("PTS_CC", unlinkingCompiler(t, "pts_method_instance_find"))
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
interface Maker {
  make<T>(x: T): Box<T>;
}
class ClassMaker implements Maker {
  make<T>(x: T): Box<T> {
    return new Box(x);
  }
}
const m: Maker = new ClassMaker();
console.log(m.make(4).value + 1);
`)

	c, err := driver.EmitC(t.Context(), entry)
	assert.NilError(t, err)
	buildErr := driver.Build(t.Context(), entry, filepath.Join(t.TempDir(), "main"))

	assert.Assert(t, strings.Contains(c, "pts_method_instance_find"), "emit-c prints AllInstances' C, which the C compiler accepts")
	var internal *diagnostic.InternalError
	assert.Assert(t, errors.As(buildErr, &internal), "expected an internal compiler error, got %v", buildErr)
	assert.Equal(t, internal.Format(filepath.Dir(entry)), "error PTS9001: internal compiler error: the generated C doesn't link: undefined reference to `pts_fn_missing'")
}

func TestAValueWhoseClassInstanceDoesntLowerCostsNoOtherValueItsInstances(t *testing.T) {
	t.Parallel()
	// Pairing useDogs' demand f<Dog[]> with the escaping arrow w makes Weird<Dog[]>, whose
	// method doesn't lower (D-094: a Dog[] returned as an Animal[]), after every trial. The
	// build fell back to DeclaredInstances for the whole program, so the unrelated arrow
	// named lost its instance (C176). Only w's instances may go.
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class Animal {
  name = "a";
}
class Dog extends Animal {
  bark = 1;
}
class Weird<T> {
  constructor(public value: T) {}
  animals(): Animal[] {
    const v = this.value;
    if (Array.isArray(v)) {
      return v;
    }
    return [];
  }
}
function useDogs(f: <T>(x: T) => Box<T>): number {
  const b: Box<Dog[]> = f([new Dog()]);
  return b.value.length;
}
function main(): void {
  const named = <T>(x: T): Box<T> => new Box(x);
  const b: Box<number> = named(5);
  const w = <T>(x: T): Weird<T> => new Weird(x);
  const d: any = w;
  console.log(b.value, d(1).animals().length, useDogs(<T>(x: T): Box<T> => new Box(x)));
}
main();
`)

	c, err := driver.EmitC(t.Context(), entry)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(c, "pts_fn_main_named_number"), "the unrelated generic arrow's instance is made")
}

func TestAFailingPairingCostsItsValueNoOtherInstance(t *testing.T) {
	t.Parallel()
	// w is passed to useNum, so it is an erased value, and useDogs' demand for Dog[] pairs with
	// it too: w's instance for Dog[] makes Weird<Dog[]>, whose animals() doesn't lower (D-094)
	// after every trial. The build fell back to DeclaredInstances for the whole program, so w's
	// instance for number, which the program's useNum(w) calls, was lost with it (C176). Only the
	// pairing of useDogs' demand with w may go.
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class Animal {
  name = "a";
}
class Dog extends Animal {
  bark = 1;
}
class Weird<T> {
  constructor(public value: T) {}
  animals(): Animal[] {
    const v = this.value;
    if (Array.isArray(v)) {
      return v;
    }
    return [];
  }
}
function useDogs(f: <T>(x: T) => Box<T>): number {
  const b: Box<Dog[]> = f([new Dog()]);
  return b.value.length;
}
function useNum(f: <T>(x: T) => Box<T>): number {
  const b: Box<number> = f(5);
  return b.value + 1;
}
function main(): void {
  const w = <T,>(x: T): Box<T> => {
    const weird = new Weird(x);
    console.log(weird.animals().length);
    return new Box(x);
  };
  console.log(useNum(w), useDogs(<T,>(x: T): Box<T> => new Box(x)));
}
main();
`)

	c, err := driver.EmitC(t.Context(), entry)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(c, "pts_fn_main_w_number"), "w's instance for useNum's demand is made")
}

func TestAFailingCallSiteCostsItsMethodNoOtherSite(t *testing.T) {
	t.Parallel()
	// The first interface call's AllInstances lowering fails at its own site (an optional argument
	// that may be undefined before a rest parameter: `converting boolean to given`), so the build
	// fell back to ValueInstances, where the second call of the same method, whose arguments lower,
	// lost its lookup by name too (C176). Only the failing site may keep its erased copy.
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
interface M {
  f<T>(a?: number, ...xs: T[]): Box<T[]>;
}
class CM implements M {
  f<T>(a?: number, ...xs: T[]): Box<T[]> {
    return new Box(xs);
  }
}
function maybe(): number | undefined {
  return undefined;
}
const m: M = new CM();
const given: Box<number[]> = m.f(1, 7, 8);
console.log(given.value.length);
try {
  const b: Box<number[]> = m.f(maybe(), 1, 2);
  console.log(b.value.length);
} catch (error: any) {
  console.log(2);
}
`)

	c, err := driver.EmitC(t.Context(), entry)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(c, "pts_method_instance_find"), "the call whose arguments lower looks its instance up by name")
}

func TestAFailureAFallbackDropsIsNotReportedBesideTheProgramsOwnError(t *testing.T) {
	t.Parallel()
	// A guard for H09b5b: the escaping arrow w's pairing with useDogs' demand makes Weird<Dog[]>,
	// whose animals() doesn't lower; DeclaredInstances doesn't make it, so it is a failure a
	// narrower fallback drops. The program has an error of its own besides, which every level
	// reports: that error alone is reported, once (as TestAProgramThatFailsEitherWayReportsItsErrorsOnce).
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class Animal {
  name = "a";
}
class Dog extends Animal {
  bark = 1;
}
class Weird<T> {
  constructor(public value: T) {}
  animals(): Animal[] {
    const v = this.value;
    if (Array.isArray(v)) {
      return v;
    }
    return [];
  }
}
function useDogs(f: <T>(x: T) => Box<T>): number {
  const b: Box<Dog[]> = f([new Dog()]);
  return b.value.length;
}
function main(): void {
  const w = <T,>(x: T): Weird<T> => new Weird(x);
  const d: any = w;
  console.log(d(1).animals().length, useDogs(<T,>(x: T): Box<T> => new Box(x)));
}
main();
debugger;
`)

	_, emitErr := driver.EmitC(t.Context(), entry)
	buildErr := driver.Build(t.Context(), entry, filepath.Join(t.TempDir(), "main"))

	for _, err := range []error{emitErr, buildErr} {
		diagnostics := compileErrors(t, err)
		assert.Equal(t, len(diagnostics), 1)
		assert.Equal(t, diagnostics[0].Format(filepath.Dir(entry)), "main.pts:30:1 - error PTS1001: debugger statement is not supported yet")
	}
}

func TestAFailureNoFallbackLevelCompilesStopsTheBuild(t *testing.T) {
	t.Parallel()
	// A guard for H09b5b: a dispatched demand (D-101) is made at every fallback level, and its
	// pairing with the override the call reaches makes Weird<Dog[]>, whose animals() doesn't
	// lower. No level compiles the program, so it isn't C176: a narrower fallback must keep it a
	// compile error, for emit-c and the build alike, not drop the pairing and build the erased
	// copy's run-time TypeError.
	entry := writeSource(t, `class Box<T> {
  constructor(public value: T) {}
}
class Animal {
  name = "a";
}
class Dog extends Animal {
  bark = 1;
}
class Weird<T> {
  constructor(public value: T) {}
  animals(): Animal[] {
    const v = this.value;
    if (Array.isArray(v)) {
      return v;
    }
    return [];
  }
}
class Base {
  make<T>(x: T): Box<T> {
    return new Box(x);
  }
}
class Sub extends Base {
  override make<T>(x: T): Box<T> {
    const weird = new Weird(x);
    console.log(weird.animals().length);
    return new Box(x);
  }
}
const b: Base = new Sub();
const dogs: Box<Dog[]> = b.make([new Dog()]);
console.log(dogs.value.length);
`)

	_, emitErr := driver.EmitC(t.Context(), entry)
	buildErr := driver.Build(t.Context(), entry, filepath.Join(t.TempDir(), "main"))

	const want = "main.pts:15:14 - error PTS1001: using a Dog[] as a Animal[] where it may be changed or kept (a copy wouldn't show the changes; a new array, or one passed to a parameter that is only read, is copied) is not supported yet"
	for _, err := range []error{emitErr, buildErr} {
		diagnostics := compileErrors(t, err)
		assert.Equal(t, len(diagnostics), 1)
		assert.Equal(t, diagnostics[0].Format(filepath.Dir(entry)), want)
		var internal *diagnostic.InternalError
		assert.Assert(t, !errors.As(err, &internal))
	}
}
