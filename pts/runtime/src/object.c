#include "pts.h"

#include <gc_gcj.h>
#include <pthread.h>
#include <stdlib.h>

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

/* find_field returns the entry of type's field (not an accessor's) for the property name, or NULL. */
static const pts_field *find_field(const pts_type *type, const char *name) {
  for (uint32_t i = 0; i < type->field_count; i++) {
    if (!pts_field_is_accessor(type->fields[i].kind) && strcmp(type->fields[i].name, name) == 0) {
      return &type->fields[i];
    }
  }
  return NULL;
}

/* A site that reads a property as check's kind met an object that holds it as another (D-072): only a compiler bug makes one. */
static _Noreturn void wrong_kind(const pts_type *type, const char *name, const pts_check *check, pts_field_kind kind, const pts_check *declared) {
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot read property %s of %s as %s: it is declared %s", name, type->name, check->name,
                pts_check_declared(kind, declared)->name);
}

void *pts_field_address_slow(const pts_object *object, pts_site *site, const char *name, const pts_check *check) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    pts_panic("TypeError", "A %s has no property '%s'", type->name, name); /* the compiler checked that it has */
  }
  if (check != NULL && field->kind != check->slot) {
    wrong_kind(type, name, check, field->kind, field->check);
  }
  if ((uintptr_t)type <= PTS_SITE_TYPE_MASK && field->offset < UINT32_C(1) << 16) {
    __atomic_store_n(&site->cache, (uint64_t)(uintptr_t)type | (uint64_t)field->offset << 48, __ATOMIC_RELAXED);
  }
  return (char *)object + field->offset;
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

pts_function pts_method_find_adapted_slow(const pts_object *object, pts_site *site, const char *name, const struct pts_check *signature,
                                          pts_function adapter) {
  const pts_type *type = object->type;
  int64_t index = method_index(type, name, PTS_METHOD);
  if (index < 0) {
    pts_panic("TypeError", "A %s has no method '%s'", type->name, name); /* the compiler or a conversion checked that it has */
  }
  if (type->methods[index].signature != signature) {
    return adapter; /* not cached: the cache holds only methods the site calls as they are */
  }
  if (index < PTS_SITE_INDEX(UINT64_MAX)) {
    cache_member(site, type, (uint64_t)index);
  }
  return type->methods[index].function;
}

pts_value pts_method_call_dynamic(pts_object *object, const char *name, uint32_t argc, const pts_value *argv) {
  int64_t index = method_index(object->type, name, PTS_METHOD);
  if (index < 0 || object->type->methods[index].dynamic == NULL) {
    pts_panic("TypeError", "A %s has no method '%s' to call through `any`", object->type->name, name); /* a conversion checked that it has */
  }
  return object->type->methods[index].dynamic(object, argc, argv);
}

/* Whether a field or accessor declared `declared` is exactly what a site of check's type writes: then the site may cache it (D-072). */
static bool exact(pts_field_kind kind, const pts_check *declared, const pts_check *check) {
  return check == NULL || pts_check_same(pts_check_declared(kind, declared), check);
}

/* Writes value, as a site of check's type holds it, to a field of a narrower declared type, converted to that type. */
static void store_converted(pts_object *object, const pts_field *field, const pts_check *check, const void *value) {
  pts_value converted = pts_storable(field->kind, field->check, pts_value_of_slot(check->slot, value), field->name, object->type);
  pts_store_value_in_slot(field->kind, (char *)object + field->offset, converted);
}

void pts_field_set_slow(pts_object *object, pts_site *site, const char *name, const pts_check *check, const void *value) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    pts_panic("TypeError", "A %s has no property '%s'", type->name, name); /* the compiler checked that it has */
  }
  if (field->kind != check->slot) {
    wrong_kind(type, name, check, field->kind, field->check);
  }
  if (!exact(field->kind, field->check, check)) {
    store_converted(object, field, check, value);
    return;
  }
  void *slot = pts_field_address_slow(object, site, name, check); /* and caches it */
  switch (pts_field_kind_size(field->kind)) { /* one store, as the site's own would be */
  case 1: __atomic_store_n((uint8_t *)slot, *(const uint8_t *)value, __ATOMIC_RELAXED); break;
  case 2: __atomic_store_n((uint16_t *)slot, *(const uint16_t *)value, __ATOMIC_RELAXED); break;
  case 4: __atomic_store_n((uint32_t *)slot, *(const uint32_t *)value, __ATOMIC_RELAXED); break;
  default: __atomic_store_n((uint64_t *)slot, *(const uint64_t *)value, __ATOMIC_RELAXED);
  }
}

pts_property pts_property_find_slow(const pts_object *object, pts_site *site, const char *name, pts_method_kind kind, const pts_check *check,
                                    const void *value) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field != NULL) {
    if (check != NULL && field->kind != check->slot) {
      wrong_kind(type, name, check, field->kind, field->check);
    }
    if (kind == PTS_SETTER && !exact(field->kind, field->check, check)) {
      store_converted((pts_object *)object, field, check, value);
      return (pts_property){NULL, NULL};
    }
    if (field->offset <= PTS_SITE_INDEX(UINT64_MAX)) {
      cache_member(site, type, field->offset);
    }
    return (pts_property){NULL, (char *)object + field->offset};
  }
  int64_t index = method_index(type, name, kind);
  if (index >= 0) {
    const pts_method *accessor = &type->methods[index];
    if (check != NULL && accessor->result != check->slot) {
      wrong_kind(type, name, check, accessor->result, accessor->check);
    }
    if (kind == PTS_SETTER && !exact(accessor->result, accessor->check, check)) {
      pts_value converted = pts_storable(accessor->result, accessor->check, pts_value_of_slot(check->slot, value), name, type);
      pts_call_setter(accessor->function, accessor->result, (pts_object *)object, converted);
      return (pts_property){NULL, NULL};
    }
    if (index < PTS_SITE_INDEX(UINT64_MAX)) {
      cache_member(site, type, (uint64_t)index | PTS_SITE_ACCESSOR >> 48);
    }
    return (pts_property){accessor->function, NULL};
  }
  if (kind == PTS_SETTER && method_index(type, name, PTS_GETTER) >= 0) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of #<%s> which has only a getter", name, type->name);
  }
  pts_panic("TypeError", "A %s has no property '%s'", type->name, name); /* the compiler checked that it has */
}

/* cache_offset caches at site that objects of type hold the property at offset, unless it doesn't fit beside the accessor flag. */
static void cache_offset(pts_site *site, const pts_type *type, uint32_t offset) {
  if (offset <= PTS_SITE_INDEX(UINT64_MAX)) {
    cache_member(site, type, offset);
  }
}

/* cache_accessor caches at site that objects of type have the getter or setter at index of their method table. */
static void cache_accessor(pts_site *site, const pts_type *type, int64_t index) {
  if (index < PTS_SITE_INDEX(UINT64_MAX)) {
    cache_member(site, type, (uint64_t)index | PTS_SITE_ACCESSOR >> 48);
  }
}

pts_value pts_optional_get_slow(const pts_object *object, pts_site *site, const char *name, const pts_check *check) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    int64_t getter = method_index(type, name, PTS_GETTER);
    if (getter < 0) {
      return PTS_VALUE_ABSENT; /* the compiler checked that no getter of another type provides it (D-066) */
    }
    const pts_method *accessor = &type->methods[getter];
    if (check != NULL && !pts_check_implies(pts_check_declared(accessor->result, accessor->check), check)) {
      wrong_kind(type, name, check, accessor->result, accessor->check);
    }
    cache_accessor(site, type, getter);
    return ((pts_value(*)(pts_object *))accessor->function)((pts_object *)object);
  }
  /* An object type that leaves the property out implies it (D-072), so a field here may be of any declared type. */
  if (check != NULL && !pts_check_implies(pts_check_declared(field->kind, field->check), check)) {
    wrong_kind(type, name, check, field->kind, field->check);
  }
  const void *slot = (const char *)object + field->offset;
  if (field->kind == PTS_FIELD_VALUE) {
    cache_offset(site, type, field->offset);
    return __atomic_load_n((const pts_value *)slot, __ATOMIC_RELAXED);
  }
  return pts_value_of_slot(field->kind, slot); /* a field of the property's type without undefined */
}

void pts_optional_set_slow(pts_object *object, pts_site *site, const char *name, const pts_check *check, pts_value value) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    int64_t setter = method_index(type, name, PTS_SETTER);
    if (setter >= 0) {
      const pts_method *accessor = &type->methods[setter];
      if (exact(accessor->result, accessor->check, check)) {
        cache_accessor(site, type, setter);
      } else {
        value = pts_storable(accessor->result, accessor->check, value, name, type);
      }
      ((void (*)(pts_object *, pts_value))accessor->function)(object, value);
      return;
    }
    if (method_index(type, name, PTS_GETTER) >= 0) {
      pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of #<%s> which has only a getter", name, type->name);
    }
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot add property %s, object is not extensible", name);
  }
  void *slot = (char *)object + field->offset;
  if (field->kind == PTS_FIELD_VALUE) {
    if (exact(field->kind, field->check, check)) {
      cache_offset(site, type, field->offset);
    } else {
      value = pts_storable(field->kind, field->check, value, name, type);
    }
    __atomic_store_n((pts_value *)slot, value, __ATOMIC_RELAXED);
    return;
  }
  if (value == PTS_VALUE_UNDEFINED) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of %s to undefined: the object's type doesn't allow it", name, type->name);
  }
  if (check != NULL) {
    value = pts_storable(field->kind, field->check, value, name, type); /* a field held as a type the property's implies (D-072) */
  }
  pts_store_value_in_slot(field->kind, slot, value);
}

bool pts_object_has(const void *object, const char *name) {
  if (name[0] == '#') {
    return false; /* a `#private` field's name is no string key */
  }
  const pts_type *type = ((const pts_object *)object)->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    return method_index(type, name, PTS_GETTER) >= 0 || method_index(type, name, PTS_SETTER) >= 0 || method_index(type, name, PTS_METHOD) >= 0;
  }
  return field->kind != PTS_FIELD_VALUE ||
         __atomic_load_n((const pts_value *)((const char *)object + field->offset), __ATOMIC_RELAXED) != PTS_VALUE_ABSENT;
}

/*
 * Object.freeze's mark (D-105): the frozen objects, in a hash table of nodes outside the
 * collected heap, so that the table keeps none of them alive. Each node's pointer is a
 * disappearing link, which the collector clears when its object dies, before the address
 * can be reused; cleared nodes are dropped when their bucket is next searched.
 */
typedef struct frozen_node {
  void *object; /* NULL once the collector cleared it */
  struct frozen_node *next;
} frozen_node;

static pthread_mutex_t frozen_lock = PTHREAD_MUTEX_INITIALIZER;
static frozen_node **frozen_buckets;
static size_t frozen_bucket_count; /* a power of two, or 0 */
static size_t frozen_count;

static size_t frozen_bucket(const void *object, size_t bucket_count) {
  uint64_t h = (uint64_t)(uintptr_t)object * 0x9E3779B97F4A7C15ull;
  return (size_t)(h ^ (h >> 32)) & (bucket_count - 1);
}

/* frozen_find returns object's node, dropping the cleared nodes of its bucket; NULL if it has none. */
static frozen_node *frozen_find(const void *object) {
  if (frozen_bucket_count == 0) {
    return NULL;
  }
  frozen_node **link = &frozen_buckets[frozen_bucket(object, frozen_bucket_count)];
  while (*link != NULL) {
    frozen_node *node = *link;
    void *frozen = node->object;
    if (frozen == object) {
      return node;
    }
    if (frozen == NULL) {
      *link = node->next; /* the collector unregistered the link when it cleared it */
      free(node);
      frozen_count--;
      continue;
    }
    link = &node->next;
  }
  return NULL;
}

static void frozen_grow(void) {
  size_t bucket_count = frozen_bucket_count == 0 ? 64 : frozen_bucket_count * 2;
  frozen_node **buckets = calloc(bucket_count, sizeof *buckets);
  if (buckets == NULL) {
    pts_panic("RangeError", "Out of memory freezing an object");
  }
  for (size_t i = 0; i < frozen_bucket_count; i++) {
    for (frozen_node *node = frozen_buckets[i], *next; node != NULL; node = next) {
      next = node->next;
      void *frozen = node->object;
      if (frozen == NULL) {
        free(node);
        frozen_count--;
        continue;
      }
      size_t b = frozen_bucket(frozen, bucket_count);
      node->next = buckets[b];
      buckets[b] = node;
    }
  }
  free(frozen_buckets);
  frozen_buckets = buckets;
  frozen_bucket_count = bucket_count;
}

/* is_primitive reports whether a value is a primitive, which JS's freezing functions take as frozen already. */
static bool is_primitive(pts_value value) {
  if (!pts_value_is_object(value)) {
    return true;
  }
  pts_kind kind = pts_value_as_object(value)->type->kind;
  return kind == PTS_KIND_STRING || kind == PTS_KIND_INT64 || kind == PTS_KIND_UINT64;
}

void pts_value_freeze(pts_value value) {
  if (is_primitive(value)) {
    return;
  }
  pts_object *object = pts_value_as_object(value);
  pthread_mutex_lock(&frozen_lock);
  if (frozen_find(object) == NULL) {
    if (frozen_count >= frozen_bucket_count) {
      frozen_grow();
    }
    frozen_node *node = malloc(sizeof *node);
    if (node == NULL) {
      pthread_mutex_unlock(&frozen_lock);
      pts_panic("RangeError", "Out of memory freezing an object");
    }
    size_t b = frozen_bucket(object, frozen_bucket_count);
    node->object = object;
    node->next = frozen_buckets[b];
    frozen_buckets[b] = node;
    frozen_count++;
    if (GC_base(object) == object) { /* a static object never dies */
      GC_general_register_disappearing_link(&node->object, object);
    }
  }
  pthread_mutex_unlock(&frozen_lock);
}

bool pts_value_is_frozen(pts_value value) {
  if (is_primitive(value)) {
    return true;
  }
  pthread_mutex_lock(&frozen_lock);
  bool frozen = frozen_find(pts_value_as_object(value)) != NULL;
  pthread_mutex_unlock(&frozen_lock);
  return frozen;
}
