package toolchain

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Target is a platform pts builds executables for (D-225). The zero Target is the
// machine pts runs on, built for with the C compiler's own default target.
type Target struct {
	Name   string // as --target and PTS_TARGET spell it
	triple string // clang's --target
	system string // the operating system's GOOS: linux or darwin
	// needs says what a cross build for it needs, for the hint when it fails.
	needs string
}

var (
	X86_64Linux  = Target{Name: "x86_64-linux", triple: "x86_64-linux-gnu", system: "linux", needs: linuxNeeds("gcc-x86-64-linux-gnu libc6-dev-amd64-cross")}
	Aarch64Linux = Target{Name: "aarch64-linux", triple: "aarch64-linux-gnu", system: "linux", needs: linuxNeeds("gcc-aarch64-linux-gnu libc6-dev-arm64-cross")}
	// Aarch64Macos is macOS on Apple silicon (H36), built for on a Mac: Apple's SDK isn't
	// redistributable, so no other machine has it. Its triple names macOS 11, the first
	// for Apple silicon, as the oldest system its executables run on.
	Aarch64Macos = Target{Name: "aarch64-macos", triple: "arm64-apple-macos11", system: "darwin", needs: "a Mac with Apple's command line tools (`xcode-select --install`)"}
)

// Targets are the targets pts builds for.
var Targets = []Target{X86_64Linux, Aarch64Linux, Aarch64Macos}

func linuxNeeds(packages string) string {
	return "clang, and the target's C library and linker: on Debian or Ubuntu, `apt install " + packages + "`"
}

// TargetNames are the targets' names, for help and errors: "x86_64-linux, aarch64-linux or aarch64-macos".
func TargetNames() string {
	names := make([]string, len(Targets))
	for i, target := range Targets {
		names[i] = target.Name
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// ParseTarget is the target called name.
func ParseTarget(name string) (Target, error) {
	for _, target := range Targets {
		if target.Name == name {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("unknown target %q: pts builds for %s", name, TargetNames())
}

// hostTarget is the target of the machine pts runs on, or the zero Target if it isn't one of Targets.
func hostTarget() Target {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return X86_64Linux
	case "linux/arm64":
		return Aarch64Linux
	case "darwin/arm64":
		return Aarch64Macos
	}
	return Target{}
}

// darwin reports whether the toolchain builds for macOS: its target's system, or this machine's.
func (t *Toolchain) darwin() bool {
	if t.Target != (Target{}) {
		return t.Target.system == "darwin"
	}
	return runtime.GOOS == "darwin"
}

// targetFromEnv is PTS_TARGET's target, or the zero Target (the host's) if it isn't set.
func targetFromEnv() (Target, error) {
	name := os.Getenv("PTS_TARGET")
	if name == "" {
		return Target{}, nil
	}
	target, err := ParseTarget(name)
	if err != nil {
		return Target{}, fmt.Errorf("PTS_TARGET: %w", err)
	}
	return target, nil
}

// cross reports whether the toolchain builds for another machine than the one it runs on.
func (t *Toolchain) cross() bool {
	return t.Target != (Target{}) && t.Target != hostTarget()
}

// targetFlags make the C compiler compile and link for the toolchain's target: none for
// the host's, so that its builds are what they were before targets; clang's --target for
// another, whose C library, libgcc and linker clang finds where Debian's and Ubuntu's
// cross packages install them (another layout takes a PTS_CC wrapper that adds clang's
// --sysroot or --gcc-toolchain). macOS's builds always name their triple, whose version is
// the oldest macOS their executables run on, where clang's default is the Mac's own.
func (t *Toolchain) targetFlags() []string {
	switch {
	case t.darwin() && !t.cross():
		return []string{"--target=" + Aarch64Macos.triple}
	case !t.cross():
		return nil
	}
	return []string{"--target=" + t.Target.triple}
}

// crossFailure is err, with what a cross build needs if err is the C compiler's failure to build the runtime for another target.
func (t *Toolchain) crossFailure(err error) error {
	var failed *CommandError
	if !t.cross() || !errors.As(err, &failed) {
		return err
	}
	return fmt.Errorf("%w\ncross-compiling for %s needs %s", err, t.Target.Name, t.Target.needs)
}
