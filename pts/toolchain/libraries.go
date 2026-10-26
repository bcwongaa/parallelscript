package toolchain

import "slices"

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

// bdwgcDarwinLibrary is bdwgc for macOS, which has no dl_iterate_phdr: the collector finds
// the program's data through dyld, and stops threads with Mach's calls (darwin_stop_world.c).
var bdwgcDarwinLibrary = func() library {
	lib := bdwgcLibrary
	lib.defines = slices.DeleteFunc(slices.Clone(lib.defines), func(define string) bool { return define == "HAVE_DL_ITERATE_PHDR" })
	return lib
}()

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

// libuvDarwinLibrary is libuv's macOS source list and defines, from its CMakeLists.txt:
// kqueue, and FSEvents, which it loads at run time, as it does the other frameworks it uses.
var libuvDarwinLibrary = library{
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
		"src/unix/proctitle.c", "src/unix/bsd-ifaddrs.c", "src/unix/kqueue.c", "src/unix/random-getentropy.c",
		"src/unix/darwin-proctitle.c", "src/unix/darwin.c", "src/unix/fsevents.c",
	},
	includes: []string{"pts/third_party/libuv/include", "pts/third_party/libuv/src"},
	defines:  []string{"_FILE_OFFSET_BITS=64", "_LARGEFILE_SOURCE", "_DARWIN_UNLIMITED_SELECT=1", "_DARWIN_USE_64_BIT_INODE=1"},
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
// .pts lines of the generated C's #line directives. Its configure's output for glibc
// Linux, the same on x86-64 and aarch64, is checked in next to it.
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

// libbacktraceDarwinLibrary is libbacktrace for macOS: its Mach-O reader, which finds
// the DWARF in the .dSYM bundle beside the executable (dsymutil's, which clang runs when it
// links a program from sources with -g), with its configure's output for macOS.
var libbacktraceDarwinLibrary = library{
	name: "backtrace",
	dir:  "pts/third_party/libbacktrace",
	sources: []string{
		"atomic.c", "dwarf.c", "fileline.c", "posix.c", "print.c", "sort.c", "state.c",
		"backtrace.c", "simple.c", "macho.c", "mmapio.c", "mmap.c",
	},
	includes: []string{"pts/third_party/libbacktrace-config-darwin", "pts/third_party/libbacktrace"},
	flags:    []string{"-funwind-tables"},
	keyFiles: []string{
		"pts/third_party/libbacktrace-config-darwin/config.h", "pts/third_party/libbacktrace-config-darwin/backtrace-supported.h",
		"pts/third_party/libbacktrace/backtrace.h", "pts/third_party/libbacktrace/internal.h",
		"pts/third_party/libbacktrace/dwarf.c", "pts/third_party/libbacktrace/macho.c",
	},
}

// PCRE2's 8-bit library, for RegExp (D-O09, D-132): Unicode support, no JIT, no 16- or
// 32-bit libraries. Its configure-free build needs config.h, pcre2.h and the character
// tables, copied next to it from its .generic and .dist files with Unicode support on.
var pcre2Library = library{
	name: "pcre2",
	dir:  "pts/third_party",
	sources: []string{
		"pcre2-config/pcre2_chartables.c",
		"pcre2/src/pcre2_auto_possess.c", "pcre2/src/pcre2_chkdint.c", "pcre2/src/pcre2_compile.c",
		"pcre2/src/pcre2_compile_cgroup.c", "pcre2/src/pcre2_compile_class.c", "pcre2/src/pcre2_config.c",
		"pcre2/src/pcre2_context.c", "pcre2/src/pcre2_convert.c", "pcre2/src/pcre2_dfa_match.c",
		"pcre2/src/pcre2_error.c", "pcre2/src/pcre2_extuni.c", "pcre2/src/pcre2_find_bracket.c",
		"pcre2/src/pcre2_jit_compile.c", "pcre2/src/pcre2_maketables.c", "pcre2/src/pcre2_match.c",
		"pcre2/src/pcre2_match_data.c", "pcre2/src/pcre2_match_next.c", "pcre2/src/pcre2_newline.c",
		"pcre2/src/pcre2_ord2utf.c", "pcre2/src/pcre2_pattern_info.c", "pcre2/src/pcre2_script_run.c",
		"pcre2/src/pcre2_serialize.c", "pcre2/src/pcre2_string_utils.c", "pcre2/src/pcre2_study.c",
		"pcre2/src/pcre2_substitute.c", "pcre2/src/pcre2_substring.c", "pcre2/src/pcre2_tables.c",
		"pcre2/src/pcre2_ucd.c", "pcre2/src/pcre2_valid_utf.c", "pcre2/src/pcre2_xclass.c",
	},
	includes: []string{"pts/third_party/pcre2-config", "pts/third_party/pcre2/src"},
	defines:  []string{"HAVE_CONFIG_H", "PCRE2_CODE_UNIT_WIDTH=8", "PCRE2_STATIC"},
	keyFiles: []string{"pts/third_party/pcre2-config/config.h", "pts/third_party/pcre2-config/pcre2.h"},
}

// mbedTLS's TLS, X.509 and crypto libraries as one archive, for fetch's https (D-223):
// its default configuration with pts_mbedtls_config.h's changes (threads, and no DTLS,
// debug log, sockets or timers). Its release tags carry the generated sources, so its
// framework submodule isn't needed.
var mbedtlsLibrary = library{
	name:     "mbedtls",
	dir:      "pts/third_party/mbedtls",
	glob:     "library/*.c",
	includes: []string{"pts/third_party/mbedtls-config", "pts/third_party/mbedtls/include"},
	defines:  []string{mbedtlsConfig},
	keyFiles: []string{"pts/third_party/mbedtls/include/mbedtls/build_info.h", "pts/third_party/mbedtls-config/pts_mbedtls_config.h"},
}

// mbedtlsConfig makes mbedTLS read pts_mbedtls_config.h after its own configuration.
const mbedtlsConfig = `MBEDTLS_USER_CONFIG_FILE="pts_mbedtls_config.h"`

// runtimeLibrary is the ParallelScript runtime itself.
var runtimeLibrary = library{
	name: "ptsrt",
	dir:  "pts/runtime",
	glob: "src/*.c",
	includes: []string{
		"pts/runtime/include", "pts/third_party/bdwgc/include", "pts/third_party/libuv/include", "pts/third_party/ryu",
		"pts/third_party/libbacktrace", "pts/third_party/pcre2-config", "pts/third_party/mbedtls-config", "pts/third_party/mbedtls/include",
	},
	defines:  []string{mbedtlsConfig},
	flags:    []string{"-std=c11", "-Wall", "-Wextra", "-Werror=implicit-function-declaration"},
	keyFiles: []string{"pts/third_party/mbedtls/include/mbedtls/build_info.h", "pts/third_party/mbedtls-config/pts_mbedtls_config.h"},
	keyDirs:  []string{"pts/runtime/include", "pts/runtime/src"},
}

// systemLibraries are linked after the archives. glibc 2.34+ folds pthread and dl into libc.
// libgcc is linked statically, so that its unwinder (libbacktrace's) adds no libgcc_s.
// macOS's libSystem, which clang always links, has them all, the unwinder too.
func (t *Toolchain) systemLibraries() []string {
	if t.darwin() {
		return nil
	}
	return []string{"-lpthread", "-ldl", "-lm", "-static-libgcc"}
}
