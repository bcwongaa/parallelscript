/*
 * Copies of memory that other threads may read or write at the same time (the
 * shared-memory contract, docs/ARCHITECTURE.md §4.13, D-143): relaxed atomic accesses of a
 * value's width or wider, never memcpy, which may copy a value in pieces.
 */
#include "pts.h"

/* One relaxed atomic load or store of size bytes (1, 2, 4 or 8) at an address aligned to size. */
uint64_t pts_load_unit(const unsigned char *at, size_t size) {
  switch (size) {
  case 1:
    return __atomic_load_n(at, __ATOMIC_RELAXED);
  case 2:
    return __atomic_load_n((const uint16_t *)at, __ATOMIC_RELAXED);
  case 4:
    return __atomic_load_n((const uint32_t *)at, __ATOMIC_RELAXED);
  default:
    return __atomic_load_n((const uint64_t *)at, __ATOMIC_RELAXED);
  }
}

void pts_store_unit(unsigned char *at, size_t size, uint64_t bits) {
  switch (size) {
  case 1:
    __atomic_store_n(at, (uint8_t)bits, __ATOMIC_RELAXED);
    return;
  case 2:
    __atomic_store_n((uint16_t *)at, (uint16_t)bits, __ATOMIC_RELAXED);
    return;
  case 4:
    __atomic_store_n((uint32_t *)at, (uint32_t)bits, __ATOMIC_RELAXED);
    return;
  default:
    __atomic_store_n((uint64_t *)at, bits, __ATOMIC_RELAXED);
  }
}

static void copy_unit(unsigned char *to, const unsigned char *from, size_t size) { pts_store_unit(to, size, pts_load_unit(from, size)); }

/* count units of size bytes, forwards or backwards; 8-byte words without a switch per unit, the bulk of most copies. */
static void copy_units(unsigned char *to, const unsigned char *from, size_t count, size_t size, bool backwards) {
  if (size == 8) {
    uint64_t *words_to = (uint64_t *)to;
    const uint64_t *words_from = (const uint64_t *)from;
    if (backwards) {
      for (size_t i = count; i-- > 0;) {
        __atomic_store_n(&words_to[i], __atomic_load_n(&words_from[i], __ATOMIC_RELAXED), __ATOMIC_RELAXED);
      }
    } else {
      for (size_t i = 0; i < count; i++) {
        __atomic_store_n(&words_to[i], __atomic_load_n(&words_from[i], __ATOMIC_RELAXED), __ATOMIC_RELAXED);
      }
    }
    return;
  }
  if (backwards) {
    for (size_t i = count; i-- > 0;) {
      copy_unit(to + i * size, from + i * size, size);
    }
  } else {
    for (size_t i = 0; i < count; i++) {
      copy_unit(to + i * size, from + i * size, size);
    }
  }
}

void pts_copy_shared(void *destination, const void *source, size_t count, size_t unit) {
  unsigned char *to = destination;
  const unsigned char *from = source;
  size_t wide = 8;
  while (wide > unit && (((uintptr_t)to ^ (uintptr_t)from) & (wide - 1)) != 0) {
    wide /= 2;
  }
  if (to > from && to < from + count) { /* overlapping, to after from: from the end */
    size_t end = count;
    for (size_t size = unit; size < wide; size *= 2) {
      if (((uintptr_t)(to + end) & size) != 0) {
        if (end < size) {
          break;
        }
        end -= size;
        copy_unit(to + end, from + end, size);
      }
    }
    size_t words = end / wide;
    end -= words * wide;
    copy_units(to + end, from + end, words, wide, true);
    for (size_t size = wide / 2; size >= unit; size /= 2) {
      if (end >= size) {
        end -= size;
        copy_unit(to + end, from + end, size);
      }
    }
    return;
  }
  size_t at = 0;
  for (size_t size = unit; size < wide; size *= 2) {
    if (((uintptr_t)(to + at) & size) != 0) {
      if (count - at < size) {
        break;
      }
      copy_unit(to + at, from + at, size);
      at += size;
    }
  }
  size_t words = (count - at) / wide;
  copy_units(to + at, from + at, words, wide, false);
  at += words * wide;
  for (size_t size = wide / 2; size >= unit; size /= 2) {
    if (count - at >= size) {
      copy_unit(to + at, from + at, size);
      at += size;
    }
  }
}

void pts_typed_array_index_error(const pts_typed_array *array, double index) {
  char buf[PTS_NUMBER_BUF_SIZE];
  pts_number_format(index, buf);
  pts_throw_new(&pts_type_builtin_RangeError, "%s index %s is out of range (length %zu)", array->type->name, buf, array->length);
}
