/* libbacktrace's config.h for x86-64 Linux with glibc, as its configure writes it with
 * clang (ParallelScript, D-069): ELF64, DWARF, dl_iterate_phdr, mmap; no compressed
 * debug sections (clang doesn't compress them by default). */
#define BACKTRACE_ELF_SIZE 64
#define BACKTRACE_XCOFF_SIZE unused
#define HAVE_ATOMIC_FUNCTIONS 1
#define HAVE_CLOCK_GETTIME 1
#define HAVE_DECL_GETPAGESIZE 1
#define HAVE_DECL_STRNLEN 1
#define HAVE_DECL__PGMPTR 0
#define HAVE_DLFCN_H 1
#define HAVE_DL_ITERATE_PHDR 1
#define HAVE_FCNTL 1
#define HAVE_GETIPINFO 1
#define HAVE_INTTYPES_H 1
#define HAVE_LINK_H 1
#define HAVE_LSTAT 1
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
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
