#include "pts.h"

#include <gc_gcj.h>
#include <pthread.h>
#include <stdio.h>
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
  pts_copy_shared(copy, cell, size, 1); /* the iteration's closures may write the cell from other threads: its value is copied whole (D-143) */
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

/*
 * A site met an object without the property or method its type promises. Only an object
 * literal asserted to a union of object types that it fits no member of can be one (D-104):
 * JS reads undefined there, which the member's type can't hold, so the read throws (D-158).
 */
static _Noreturn void missing_member(const pts_type *type, const char *action, const char *name) {
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot %s '%s' of #<%s>: it has none, though a type assertion says it has", action, name,
                pts_unfrozen(type)->name);
}

/*
 * A site that reads a property as check's type met an object that holds it as another
 * type, of which the value isn't one as it is (D-072, D-192): an erased copy's object that
 * a union took as a member it fits only by leaving properties out (C187), or a lie through `any`.
 */
static _Noreturn void wrong_kind(const pts_type *type, const char *name, const pts_check *check, pts_field_kind kind, const pts_check *declared) {
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot read property %s of %s as %s: it is declared %s", name, type->name, check->name,
                pts_check_declared(kind, declared)->name);
}

/*
 * Whether a field or getter of kind `kind` declared `declared` holds only values of the
 * type check that a site reads it as, in check's representation, so that the site may read
 * it as it is and cache it. Kinds alone don't say so: an object an erased copy made holds
 * an `any[]` where its type's other member holds a `number[]` (C187, D-192). A field of
 * the runtime's own descriptors, which declare no types, is one of the type its kind holds.
 * An object read as an object type is read as it is, whatever its declared type: its
 * properties are only read through sites, which verify each one as it is read.
 */
static bool reads_as_is(pts_field_kind kind, const pts_check *declared, const pts_check *check) {
  if (check == NULL) {
    return true;
  }
  if (kind != check->slot) {
    return false;
  }
  if (declared == NULL || declared == check) {
    return true;
  }
  if (check->kind == PTS_CHECK_OBJECT && !check->result && (declared->kind == PTS_CHECK_OBJECT || declared->kind == PTS_CHECK_INSTANCE)) {
    return true;
  }
  return pts_check_implies(declared, check);
}

/*
 * A read, through a site of check's type, of a field's or getter's value whose declared
 * type doesn't say it is one: in a new cell the site reads, the value as check's
 * representation holds it, if it is one as it is (a dynamic value of the type, an object
 * of an object type by its declared types, C186; a closure of another type adapted); else
 * TypeError, never a misread (C187). Nothing is copied: a write into a copy would be lost.
 * An unset field (D-141) reads as unset, which the site reports.
 */
static void *read_converted(const pts_type *type, const char *name, const pts_check *check, pts_field_kind kind, const pts_check *declared,
                            pts_value value) {
  bool pointers = check->slot == PTS_FIELD_VALUE || check->slot == PTS_FIELD_STRING || check->slot == PTS_FIELD_OBJECT;
  void *cell = pts_cell_new(sizeof(pts_value), pointers);
  if (value == PTS_VALUE_UNDEFINED && kind == check->slot && (kind == PTS_FIELD_OBJECT || kind == PTS_FIELD_STRING)) {
    return cell; /* NULL */
  }
  pts_value converted;
  if (!pts_value_try_convert_as_is(value, check, &converted)) {
    wrong_kind(type, name, check, kind, declared);
  }
  pts_store_value_in_slot(check->slot, cell, converted);
  return cell;
}

void *pts_field_address_slow(const pts_object *object, pts_site *site, const char *name, const pts_check *check) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    missing_member(type, "read property", name);
  }
  void *slot = (char *)object + field->offset;
  if (!reads_as_is(field->kind, field->check, check)) {
    return read_converted(type, name, check, field->kind, field->check, pts_value_of_slot(field->kind, slot));
  }
  if ((uintptr_t)type <= PTS_SITE_TYPE_MASK && field->offset < UINT32_C(1) << 16) {
    __atomic_store_n(&site->cache, (uint64_t)(uintptr_t)type | (uint64_t)field->offset << 48, __ATOMIC_RELAXED);
  }
  return slot;
}

void *pts_field_store_address_slow(const pts_object *object, pts_site *site, const char *name, const pts_check *check) {
  const pts_type *type = object->type;
  const pts_field *field = find_field(type, name);
  if (field == NULL) {
    pts_throw_not_extensible(name); /* an asserted object without it (D-158): JS would add it */
  }
  if (type->frozen) {
    pts_throw_frozen_field(object, name);
  }
  if (check != NULL && field->kind != check->slot) { /* only PTS_FIELD_SET's sites pass a check for a store (D-073) */
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
    missing_member(type, "call method", name);
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
    missing_member(type, "call method", name);
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

pts_function pts_instance_find(const void *value, const char *name, const pts_check *signature) {
  for (const pts_type *type = ((const pts_object *)value)->type; type != NULL; type = type->parent) {
    for (uint32_t i = 0; i < type->instance_count; i++) {
      const pts_instance *instance = &type->instances[i];
      if (instance->signature == signature && (name == NULL ? instance->name == NULL : instance->name != NULL && strcmp(instance->name, name) == 0)) {
        return instance->function;
      }
    }
  }
  return NULL;
}

pts_function pts_method_instance_find(const pts_object *object, pts_site *site, const char *name, const pts_check *signature, pts_closure **closure) {
  *closure = NULL;
  const pts_type *type = object->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  uint64_t offset = cache >> 48; /* of the field holding the closure, or 0 for none */
  if ((uintptr_t)type != (cache & PTS_SITE_TYPE_MASK)) {
    for (const pts_type *class = type; class != NULL; class = class->parent) {
      for (uint32_t i = 0; i < class->instance_count; i++) {
        const pts_instance *instance = &class->instances[i];
        if (instance->signature == signature && instance->name != NULL && strcmp(instance->name, name) == 0) {
          return instance->function; /* NULL ends the search: the nearer class's method has no instance of signature */
        }
      }
    }
    const pts_field *field = type->kind == PTS_KIND_OBJECT ? find_field(type, name) : NULL;
    offset = field != NULL && field->kind == PTS_FIELD_OBJECT ? field->offset : 0;
    cache_member(site, type, offset); /* what a descriptor's classes list doesn't change */
  }
  if (offset == 0) {
    return NULL;
  }
  pts_closure *method = __atomic_load_n((pts_closure *const *)((const char *)object + offset), __ATOMIC_RELAXED);
  if (method == NULL || method->type->kind != PTS_KIND_FUNCTION) {
    return NULL;
  }
  pts_function function = pts_instance_find(method, name, signature);
  if (function != NULL) {
    *closure = method;
  }
  return function;
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
    pts_throw_not_extensible(name); /* an asserted object without it (D-158): JS would add it */
  }
  if (type->frozen) {
    pts_throw_frozen_field(object, name);
  }
  if (!exact(field->kind, field->check, check)) { /* a narrower declared type, or another kind (an erased copy's dynamic field, C186) */
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
    if (kind == PTS_SETTER && type->frozen) {
      pts_throw_frozen_field(object, name); /* a frozen object's setters still run */
    }
    if (kind == PTS_GETTER && !reads_as_is(field->kind, field->check, check)) {
      return (pts_property){NULL, read_converted(type, name, check, field->kind, field->check, pts_value_of_slot(field->kind, (const char *)object + field->offset))};
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
    if (kind == PTS_GETTER && !reads_as_is(accessor->result, accessor->check, check)) {
      pts_value result = pts_call_getter(accessor->function, accessor->result, (pts_object *)object);
      return (pts_property){NULL, read_converted(type, name, check, accessor->result, accessor->check, result)};
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
  if (kind == PTS_SETTER) {
    pts_throw_not_extensible(name); /* an asserted object without it (D-158): JS would add it */
  }
  missing_member(type, "read property", name);
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

/*
 * An optional property's value read through a site of check's type from a field or getter
 * whose declared type doesn't say it is one (read_converted): itself if it is absent or one
 * as it is, uncached; else TypeError.
 */
static pts_value optional_converted(const pts_type *type, const char *name, const pts_check *check, pts_field_kind kind, const pts_check *declared,
                                    pts_value value) {
  pts_value converted;
  if (value == PTS_VALUE_ABSENT) {
    return value;
  }
  if (!pts_value_try_convert_as_is(value, check, &converted)) {
    wrong_kind(type, name, check, kind, declared);
  }
  return converted;
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
      return optional_converted(type, name, check, accessor->result, accessor->check, pts_call_getter(accessor->function, accessor->result, (pts_object *)object));
    }
    cache_accessor(site, type, getter);
    return pts_call_getter(accessor->function, accessor->result, (pts_object *)object);
  }
  /* An object type that leaves the property out implies it (D-072), so a field here may be of any declared type. */
  const void *slot = (const char *)object + field->offset;
  if (check != NULL && !pts_check_implies(pts_check_declared(field->kind, field->check), check)) {
    pts_value value = field->kind == PTS_FIELD_VALUE ? __atomic_load_n((const pts_value *)slot, __ATOMIC_RELAXED) : pts_value_of_slot(field->kind, slot);
    return optional_converted(type, name, check, field->kind, field->check, value); /* absent stays absent */
  }
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
      pts_call_setter(accessor->function, accessor->result, object, value);
      return;
    }
    if (method_index(type, name, PTS_GETTER) >= 0) {
      pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of #<%s> which has only a getter", name, type->name);
    }
    pts_throw_not_extensible(name);
  }
  if (type->frozen) {
    pts_throw_frozen_field(object, name);
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

/* The key of an iterator result's property found by name: done or value, the names typed code reads by name, without allocating. */
static pts_value result_key(const char *name) {
  PTS_STATIC_STRING(done, 1, 4, 4, "done");
  PTS_STATIC_STRING(value, 1, 5, 5, "value");
  if (strcmp(name, "done") == 0) {
    return pts_value_from_string(done);
  }
  if (strcmp(name, "value") == 0) {
    return pts_value_from_string(value);
  }
  return pts_value_from_string(pts_string_from_cstr(name));
}

pts_value pts_result_get_slow(const pts_object *object, pts_site *site, const char *name) {
  const pts_type *type = object->type;
  const pts_field *field = type->kind == PTS_KIND_OBJECT ? find_field(type, name) : NULL;
  if (field != NULL) {
    const void *slot = (const char *)object + field->offset;
    if (field->kind == PTS_FIELD_VALUE) {
      cache_offset(site, type, field->offset);
      return pts_value_present(__atomic_load_n((const pts_value *)slot, __ATOMIC_RELAXED));
    }
    return pts_value_of_slot(field->kind, slot);
  }
  if (type->kind == PTS_KIND_OBJECT) {
    int64_t getter = method_index(type, name, PTS_GETTER);
    if (getter >= 0) {
      return pts_call_getter(type->methods[getter].function, type->methods[getter].result, (pts_object *)object);
    }
    if (method_index(type, name, PTS_METHOD) < 0) {
      return PTS_VALUE_UNDEFINED; /* an object without it: a result without done, say */
    }
  }
  /* a class's method, bound as D-144 binds it, or another kind of object's property: found as through `any` */
  return pts_dynamic_get(pts_value_from_object((pts_object *)object), result_key(name));
}

void pts_result_set(pts_object *object, const char *name, pts_value value) {
  pts_dynamic_set(pts_value_from_object(object), pts_value_from_string(pts_string_from_cstr(name)), value);
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

/* ---------------------------------------------------------------- Object.freeze (D-105, D-157) */

/* How V8's messages name an object: [object Array], [object RegExp], an error's string, #<Point> for an instance of a class. */
static void describe(const pts_object *object, char *buffer, size_t size) {
  const pts_type *type = pts_unfrozen(object->type);
  switch (type->kind) {
  case PTS_KIND_ARRAY:
  case PTS_KIND_TUPLE:
    snprintf(buffer, size, "[object Array]");
    return;
  case PTS_KIND_REGEXP:
    snprintf(buffer, size, "[object RegExp]");
    return;
  case PTS_KIND_RECORD:
    snprintf(buffer, size, type->name[0] == '[' ? "[object Object]" : "#<Object>"); /* a match's groups have a null prototype */
    return;
  default:
    break;
  }
  if (pts_is_error(object)) {
    pts_string *text = pts_error_to_string((pts_error *)object);
    snprintf(buffer, size, "%.*s", (int)text->byte_len, text->bytes);
    return;
  }
  snprintf(buffer, size, "#<%s>", type->name);
}

void pts_throw_read_only(const pts_object *object, const char *property) {
  char described[256];
  describe(object, described, sizeof described);
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot assign to read only property '%s' of object '%s'", property, described);
}

void pts_throw_not_extensible(const char *property) {
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot add property %s, object is not extensible", property);
}

void pts_throw_undeletable(const pts_object *object, const char *property) {
  char described[256];
  describe(object, described, sizeof described);
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot delete property '%s' of %s", property, described);
}

void pts_throw_frozen_field(const pts_object *object, const char *name) {
  const pts_field *field = find_field(object->type, name);
  if (field != NULL && field->kind == PTS_FIELD_VALUE &&
      __atomic_load_n((const pts_value *)((const char *)object + field->offset), __ATOMIC_RELAXED) == PTS_VALUE_ABSENT) {
    pts_throw_not_extensible(name); /* an optional property it doesn't have */
  }
  pts_throw_read_only(object, name);
}

/*
 * Frozen twins (D-157): one per descriptor, made when the first object of its type is
 * frozen and never freed, since frozen objects point at it for as long as they live.
 */
typedef struct twin_node {
  const pts_type *original;
  const pts_type *twin;
  struct twin_node *next;
} twin_node;

static pthread_mutex_t frozen_lock = PTHREAD_MUTEX_INITIALIZER;
static twin_node *twins;

/* frozen_twin returns type's frozen twin, making it if there is none yet; under frozen_lock. */
static const pts_type *frozen_twin(const pts_type *type) {
  for (twin_node *node = twins; node != NULL; node = node->next) {
    if (node->original == type) {
      return node->twin;
    }
  }
  size_t size = type->descriptor_size != 0 ? type->descriptor_size : sizeof(pts_type);
  pts_type *twin = malloc(size); /* a class's method table comes with it: virtual calls read it through the object's descriptor */
  twin_node *node = malloc(sizeof *node);
  if (twin == NULL || node == NULL) {
    pthread_mutex_unlock(&frozen_lock);
    pts_panic("RangeError", "Out of memory freezing an object");
  }
  memcpy(twin, type, size);
  twin->parent = type; /* so instanceof, and every walk up the classes, finds the original first */
  twin->frozen = true;
  *node = (twin_node){type, twin, twins};
  twins = node;
  return twin;
}

/*
 * Whether freezing an object of type gives it a frozen twin: the kinds whose properties
 * or elements JS makes read only. The others keep their descriptors, which the runtime
 * compares (a Map's entries, a Date's time and a function's code stay writable in JS), and
 * so does an object without fields, which has nothing to write.
 */
static bool takes_twin(const pts_type *type) {
  switch (type->kind) {
  case PTS_KIND_ARRAY:
  case PTS_KIND_TUPLE:
  case PTS_KIND_RECORD:
  case PTS_KIND_REGEXP:
    return true;
  case PTS_KIND_OBJECT:
    return type->field_count > 0;
  default:
    return false;
  }
}

/*
 * The mark of frozen objects without a twin (D-105): a hash table of nodes outside the
 * collected heap, so that the table keeps none of them alive. Each node's pointer is a
 * disappearing link, which the collector clears when its object dies, before the address
 * can be reused; cleared nodes are dropped when their bucket is next searched.
 */
typedef struct frozen_node {
  void *object; /* NULL once the collector cleared it */
  struct frozen_node *next;
} frozen_node;

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
  const pts_type *type = object->type;
  if (type->frozen) {
    return;
  }
  if (type->kind == PTS_KIND_TYPED_ARRAY && ((const pts_typed_array *)object)->length > 0) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot freeze array buffer views with elements");
  }
  pthread_mutex_lock(&frozen_lock);
  if (takes_twin(type)) {
    __atomic_store_n(&object->type, frozen_twin(type), __ATOMIC_RELEASE);
  } else if (frozen_find(object) == NULL) {
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
  const pts_type *type = pts_value_as_object(value)->type;
  if (type->frozen || takes_twin(type)) {
    return type->frozen;
  }
  pthread_mutex_lock(&frozen_lock);
  bool frozen = frozen_find(pts_value_as_object(value)) != NULL;
  pthread_mutex_unlock(&frozen_lock);
  return frozen;
}
