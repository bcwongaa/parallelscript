package toolchain

// library describes one static archive built from C sources with the toolchain's C compiler.
type library struct {
	name     string   // the archive is lib<name>.a
	dir      string   // source root, relative to the PTS home
	sources  []string // relative to dir
	glob     string   // alternatively, a glob relative to dir, expanded at build time
	includes []string // relative to the PTS home
	defines  []string
	flags    []string
	// keyFiles and keyDirs (relative to the PTS home) are hashed into the cache key.
	// Vendored libraries are keyed by their version headers; the runtime by all its sources.
	keyFiles []string
	keyDirs  []string
}

// Flags shared by every build of the vendored libraries. They are always optimised,
// even for sanitizer builds of the runtime: their own correctness isn't under test here.
var vendoredFlags = []string{"-O2", "-g", "-fPIC", "-w"}

// bdwgc as one translation unit (extra/gc.c), configured like its CMake defaults
// for a threaded build with parallel marking, thread-local allocation and gcj-style
// allocation, which objects use (D-L13, D-024).
var bdwgcLibrary = library{
	name:     "gc",
	dir:      "pts/third_party/bdwgc",
	sources:  []string{"extra/gc.c"},
	includes: []string{"pts/third_party/bdwgc/include"},
	defines: []string{
		"GC_THREADS", "PARALLEL_MARK", "THREAD_LOCAL_ALLOC", "GC_BUILTIN_ATOMIC", "GC_GCJ_SUPPORT", "GC_ENABLE_SUSPEND_THREAD",
		"ALL_INTERIOR_POINTERS", "NO_EXECUTE_PERMISSION", "HANDLE_FORK", "ENABLE_DISCLAIM",
		"GC_ATOMIC_UNCOLLECTABLE", "HAVE_DLADDR", "HAVE_DL_ITERATE_PHDR", "USE_MMAP", "USE_MUNMAP",
		"_REENTRANT", "NDEBUG",
	},
	flags:    []string{"-fno-strict-aliasing"},
	keyFiles: []string{"pts/third_party/bdwgc/include/gc_version.h"},
}

// libuv's Linux source list and defines, from its CMakeLists.txt.
var libuvLibrary = library{
	name: "uv",
	dir:  "pts/third_party/libuv",
	sources: []string{
		"src/fs-poll.c", "src/idna.c", "src/inet.c", "src/random.c", "src/strscpy.c", "src/strtok.c",
		"src/thread-common.c", "src/threadpool.c", "src/timer.c", "src/uv-common.c",
		"src/uv-data-getter-setters.c", "src/version.c",
		"src/unix/async.c", "src/unix/core.c", "src/unix/dl.c", "src/unix/fs.c", "src/unix/getaddrinfo.c",
		"src/unix/getnameinfo.c", "src/unix/loop-watcher.c", "src/unix/loop.c", "src/unix/pipe.c",
		"src/unix/poll.c", "src/unix/process.c", "src/unix/random-devurandom.c", "src/unix/signal.c",
		"src/unix/stream.c", "src/unix/tcp.c", "src/unix/thread.c", "src/unix/tty.c", "src/unix/udp.c",
		"src/unix/proctitle.c", "src/unix/linux.c", "src/unix/procfs-exepath.c",
		"src/unix/random-getrandom.c", "src/unix/random-sysctl-linux.c",
	},
	includes: []string{"pts/third_party/libuv/include", "pts/third_party/libuv/src"},
	defines:  []string{"_FILE_OFFSET_BITS=64", "_LARGEFILE_SOURCE", "_GNU_SOURCE", "_POSIX_C_SOURCE=200112"},
	flags:    []string{"-fno-strict-aliasing"},
	keyFiles: []string{"pts/third_party/libuv/include/uv/version.h"},
}

// Ryu's shortest round-trip double-to-string conversion (d2s), for Number::toString.
// It has no version header, so the cache key hashes the sources it compiles.
var ryuLibrary = library{
	name:     "ryu",
	dir:      "pts/third_party/ryu",
	sources:  []string{"ryu/d2s.c"},
	includes: []string{"pts/third_party/ryu"},
	keyFiles: []string{
		"pts/third_party/ryu/ryu/d2s.c", "pts/third_party/ryu/ryu/ryu.h", "pts/third_party/ryu/ryu/common.h",
		"pts/third_party/ryu/ryu/d2s.h", "pts/third_party/ryu/ryu/d2s_full_table.h",
		"pts/third_party/ryu/ryu/d2s_intrinsics.h", "pts/third_party/ryu/ryu/digit_table.h",
	},
}

// libbacktrace captures and symbolizes the stack frames of errors (D-069): unwinding with
// libgcc's unwinder, reading the program's own DWARF, whose line tables map code to the
// .pts lines of the generated C's #line directives. Its configure's output for x86-64
// Linux is checked in next to it.
var libbacktraceLibrary = library{
	name: "backtrace",
	dir:  "pts/third_party/libbacktrace",
	sources: []string{
		"atomic.c", "dwarf.c", "fileline.c", "posix.c", "print.c", "sort.c", "state.c",
		"backtrace.c", "simple.c", "elf.c", "mmapio.c", "mmap.c",
	},
	includes: []string{"pts/third_party/libbacktrace-config", "pts/third_party/libbacktrace"},
	flags:    []string{"-funwind-tables"},
	keyFiles: []string{
		"pts/third_party/libbacktrace-config/config.h", "pts/third_party/libbacktrace-config/backtrace-supported.h",
		"pts/third_party/libbacktrace/backtrace.h", "pts/third_party/libbacktrace/internal.h",
		"pts/third_party/libbacktrace/dwarf.c", "pts/third_party/libbacktrace/elf.c",
	},
}

// runtimeLibrary is the ParallelScript runtime itself.
var runtimeLibrary = library{
	name:     "ptsrt",
	dir:      "pts/runtime",
	glob:     "src/*.c",
	includes: []string{"pts/runtime/include", "pts/third_party/bdwgc/include", "pts/third_party/libuv/include", "pts/third_party/ryu", "pts/third_party/libbacktrace"},
	flags:    []string{"-std=c11", "-Wall", "-Wextra", "-Werror=implicit-function-declaration"},
	keyDirs:  []string{"pts/runtime/include", "pts/runtime/src"},
}

// systemLibraries are linked after the archives. glibc 2.34+ folds pthread and dl into libc.
// libgcc is linked statically, so that its unwinder (libbacktrace's) adds no libgcc_s.
var systemLibraries = []string{"-lpthread", "-ldl", "-lm", "-static-libgcc"}
