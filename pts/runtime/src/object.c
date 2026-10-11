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
