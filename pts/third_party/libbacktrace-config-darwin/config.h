/* libbacktrace's config.h for macOS on Apple silicon, as its configure writes it with Apple's
 * clang (ParallelScript, H36): Mach-O, DWARF from the executable's .dSYM bundle, dyld's image
 * list for the loaded files, mmap; libSystem's unwinder declares _Unwind_GetIPInfo. */
#define BACKTRACE_ELF_SIZE unused
#define BACKTRACE_XCOFF_SIZE unused
#define HAVE_ATOMIC_FUNCTIONS 1
#define HAVE_CLOCK_GETTIME 1
#define HAVE_DECL_GETPAGESIZE 1
#define HAVE_DECL_STRNLEN 1
#define HAVE_DECL__PGMPTR 0
#define HAVE_DLFCN_H 1
#define HAVE_FCNTL 1
#define HAVE_GETIPINFO 1
#define HAVE_INTTYPES_H 1
#define HAVE_LSTAT 1
#define HAVE_MACH_O_DYLD_H 1
#define HAVE_MEMORY_H 1
#define HAVE_READLINK 1
#define HAVE_STDINT_H 1
#define HAVE_STDLIB_H 1
#define HAVE_STRINGS_H 1
#define HAVE_STRING_H 1
#define HAVE_SYNC_FUNCTIONS 1
#define HAVE_SYS_MMAN_H 1
#define HAVE_SYS_STAT_H 1
#define HAVE_SYS_TYPES_H 1
#define HAVE_UNISTD_H 1
#define STDC_HEADERS 1
#ifndef _DARWIN_C_SOURCE
#define _DARWIN_C_SOURCE 1
#endif
