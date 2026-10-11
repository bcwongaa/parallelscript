#include "pts.h"

#include <gc_gcj.h>

/*
 * Instances that hold pointers are allocated gcj-style: bdwgc stores the type pointer
 * in the first word and scans only the words the type's gc_descr names, so a number
 * field is never mistaken for a pointer. Pointer-free instances are atomic, never scanned.
 * Both kinds come from thread-local free lists.
 */
void *pts_object_new(const pts_type *type) {
  pts_object *object;
  if (type->gc_descr == PTS_GC_NO_POINTERS) {
    object = GC_MALLOC_ATOMIC(type->size);
    if (object != NULL) {
      memset(object, 0, type->size);
      object->type = type;
    }
  } else {
    object = GC_GCJ_MALLOC(type->size, (void *)type);
  }
  if (object == NULL) {
    pts_panic("RangeError", "Out of memory allocating a %s of %u bytes", type->name, type->size);
  }
  return object;
}

void *pts_cell_new(size_t size, bool holds_pointers) {
  void *cell = holds_pointers ? GC_MALLOC(size) : GC_MALLOC_ATOMIC(size);
  if (cell == NULL) {
    pts_panic("RangeError", "Out of memory allocating a variable of %zu bytes", size);
  }
  memset(cell, 0, size);
  return cell;
}

void *pts_cell_copy(const void *cell, size_t size, bool holds_pointers) {
  void *copy = pts_cell_new(size, holds_pointers);
  memcpy(copy, cell, size);
  return copy;
}

void *pts_field_address_slow(const pts_object *object, pts_site *site, const char *name) {
  const pts_type *type = object->type;
  for (uint32_t i = 0; i < type->field_count; i++) {
    if (strcmp(type->fields[i].name, name) == 0) {
      uint32_t offset = type->fields[i].offset;
      if ((uintptr_t)type <= PTS_SITE_TYPE_MASK && offset < UINT32_C(1) << 16) {
        __atomic_store_n(&site->cache, (uint64_t)(uintptr_t)type | (uint64_t)offset << 48, __ATOMIC_RELAXED);
      }
      return (char *)object + offset;
    }
  }
  pts_panic("TypeError", "A %s has no property '%s'", type->name, name); /* the compiler checked that it has */
}

/* method_index returns the index in type's method table of the method name of the given kind, or -1. */
static int64_t method_index(const pts_type *type, const char *name, pts_method_kind kind) {
  for (uint32_t i = 0; i < type->method_count; i++) {
    if (type->methods[i].kind == kind && strcmp(type->methods[i].name, name) == 0) {
      return i;
    }
  }
  return -1;
}

/* cache_member caches at site that objects of type have what payload says (an offset, or an index with flags). */
static void cache_member(pts_site *site, const pts_type *type, uint64_t payload) {
  if ((uintptr_t)type <= PTS_SITE_TYPE_MASK && payload < UINT64_C(1) << 16) {
    __atomic_store_n(&site->cache, (uint64_t)(uintptr_t)type | payload << 48, __ATOMIC_RELAXED);
  }
}

pts_function pts_method_find_slow(const pts_object *object, pts_site *site, const char *name, pts_method_kind kind) {
  const pts_type *type = object->type;
  int64_t index = method_index(type, name, kind);
  if (index < 0) {
    pts_panic("TypeError", "A %s has no method '%s'", type->name, name); /* the compiler checked that it has */
  }
  if (index < PTS_SITE_INDEX(UINT64_MAX)) {
    cache_member(site, type, (uint64_t)index);
  }
  return type->methods[index].function;
}

pts_property pts_property_find_slow(const pts_object *object, pts_site *site, const char *name, pts_method_kind kind) {
  const pts_type *type = object->type;
  for (uint32_t i = 0; i < type->field_count; i++) {
    if (strcmp(type->fields[i].name, name) == 0) {
      uint32_t offset = type->fields[i].offset;
      if (offset <= PTS_SITE_INDEX(UINT64_MAX)) {
        cache_member(site, type, offset);
      }
      return (pts_property){NULL, (char *)object + offset};
    }
  }
  int64_t index = method_index(type, name, kind);
  if (index >= 0) {
    if (index < PTS_SITE_INDEX(UINT64_MAX)) {
      cache_member(site, type, (uint64_t)index | PTS_SITE_ACCESSOR >> 48);
    }
    return (pts_property){type->methods[index].function, NULL};
  }
  if (kind == PTS_SETTER && method_index(type, name, PTS_GETTER) >= 0) {
    pts_panic("TypeError", "Cannot set property %s of #<%s> which has only a getter", name, type->name);
  }
  pts_panic("TypeError", "A %s has no property '%s'", type->name, name); /* the compiler checked that it has */
}
