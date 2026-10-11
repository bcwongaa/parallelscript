/*
 * Converting dynamic values to the program's types (D-071). A value that already is one
 * of the target type is returned as it is, so a typed object that went through `any`
 * keeps its identity; dynamic data (an `any[]`, a hash-map object of `any`, parsed JSON)
 * is checked and copied into the target's representation, since an `any[]` holds
 * pts_values where a `number[]` holds doubles. Whether an object already is one is
 * decided by the declared types of its fields (D-072), not by the values they hold now,
 * which a write through the object's own type could change.
 */
#include "pts.h"

#include <stdio.h>

enum { MAX_DEPTH = 256 };

/* The objects being converted, with their targets: meeting one again is a cycle. */
typedef struct converting {
  struct {
    const pts_object *object;
    const pts_check *check;
  } stack[MAX_DEPTH];
  size_t depth;
} converting;

static bool convert(converting *c, pts_value value, const pts_check *check, bool copy, pts_value *out);

/* A value of kind slot in C storage (a slot of an object or array), from a pts_value already checked to fit it. */
typedef union slot_value {
  uint64_t bits;
  pts_value value;
} slot_value;

static bool is_kind(pts_value value, pts_kind kind) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == kind;
}

static const pts_field *field_named(const pts_type *type, const char *name) {
  for (uint32_t i = 0; i < type->field_count; i++) {
    if (!pts_field_is_accessor(type->fields[i].kind) && strcmp(type->fields[i].name, name) == 0) {
      return &type->fields[i];
    }
  }
  return NULL;
}

static const pts_method *getter_named(const pts_type *type, const char *name) {
  for (uint32_t i = 0; i < type->method_count; i++) {
    if (type->methods[i].kind == PTS_GETTER && strcmp(type->methods[i].name, name) == 0) {
      return &type->methods[i];
    }
  }
  return NULL;
}

/* Pushes (object, check), unless it is being converted already (a cycle) or the walk is too deep. */
static bool enter(converting *c, const pts_object *object, const pts_check *check) {
  for (size_t i = 0; i < c->depth; i++) {
    if (c->stack[i].object == object && c->stack[i].check == check) {
      return false;
    }
  }
  if (c->depth == MAX_DEPTH) {
    return false;
  }
  c->stack[c->depth].object = object;
  c->stack[c->depth].check = check;
  c->depth++;
  return true;
}

/* ---------------------------------------------------------------- declared types */

/* The types of places whose check is NULL: the types their kinds hold. */
static const pts_check kind_checks[] = {
    [PTS_FIELD_NUMBER] = {.kind = PTS_CHECK_NUMBER, .slot = PTS_FIELD_NUMBER, .name = "number"},
    [PTS_FIELD_BOOL] = {.kind = PTS_CHECK_BOOLEAN, .slot = PTS_FIELD_BOOL, .name = "boolean"},
    [PTS_FIELD_STRING] = {.kind = PTS_CHECK_STRING, .slot = PTS_FIELD_STRING, .name = "string"},
    [PTS_FIELD_VALUE] = {.kind = PTS_CHECK_ANY, .slot = PTS_FIELD_VALUE, .name = "any"},
    [PTS_FIELD_OBJECT] = {.kind = PTS_CHECK_OPAQUE, .slot = PTS_FIELD_OBJECT, .name = "object"},
    [PTS_FIELD_I8] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_I8, .name = "i8"},
    [PTS_FIELD_I16] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_I16, .name = "i16"},
    [PTS_FIELD_I32] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_I32, .name = "i32"},
    [PTS_FIELD_I64] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_I64, .name = "i64"},
    [PTS_FIELD_U8] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_U8, .name = "u8"},
    [PTS_FIELD_U16] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_U16, .name = "u16"},
    [PTS_FIELD_U32] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_U32, .name = "u32"},
    [PTS_FIELD_U64] = {.kind = PTS_CHECK_INTEGER, .slot = PTS_FIELD_U64, .name = "u64"},
    [PTS_FIELD_F32] = {.kind = PTS_CHECK_NUMBER, .slot = PTS_FIELD_F32, .name = "f32"},
};

const pts_check *pts_check_declared(pts_field_kind kind, const pts_check *check) { return check != NULL ? check : &kind_checks[kind]; }

/* Checks of the program's types are one per type; the runtime's of primitives may repeat them. */
bool pts_check_same(const pts_check *a, const pts_check *b) {
  if (a == b) {
    return true;
  }
  switch (a->kind) {
  case PTS_CHECK_ANY:
  case PTS_CHECK_NUMBER:
  case PTS_CHECK_INTEGER:
  case PTS_CHECK_STRING:
  case PTS_CHECK_BOOLEAN:
  case PTS_CHECK_NULL:
  case PTS_CHECK_UNDEFINED:
    return a->kind == b->kind && a->slot == b->slot;
  default:
    return false;
  }
}

/* The pairs of types whose implication is being decided: meeting one again is a cycle of recursive types, which holds (coinduction). */
typedef struct implying {
  struct {
    const pts_check *sub, *super;
  } stack[MAX_DEPTH];
  size_t depth;
} implying;

static bool implies(implying *s, const pts_check *sub, const pts_check *super);

/*
 * Whether a place of kind `kind` declared `declared` (a field, or a getter's result) can
 * stand for a property `property` of an object type: it holds the property's
 * representation and only values of its type, or for an optional property, values of
 * its type without undefined, which the property's sites convert (D-066).
 */
static bool place_implies(implying *s, pts_field_kind kind, const pts_check *declared, const pts_check_property *property) {
  if (kind != property->check->slot && !(property->optional && kind != PTS_FIELD_VALUE)) {
    return false;
  }
  return implies(s, declared, property->check);
}

static bool type_extends(const pts_type *type, const pts_type *ancestor) {
  for (; type != NULL; type = type->parent) {
    if (type == ancestor) {
      return true;
    }
  }
  return false;
}

/* Whether every object of descriptor `type` has every property of the object type `super` as it is (by its declared types). */
static bool descriptor_implies(implying *s, const pts_type *type, const pts_check *super) {
  for (uint32_t i = 0; i < super->count; i++) {
    const pts_check_property *property = &super->properties[i];
    const pts_field *field = field_named(type, property->name);
    if (field != NULL) {
      if (!place_implies(s, field->kind, pts_check_declared(field->kind, field->check), property)) {
        return false;
      }
      continue;
    }
    const pts_method *getter = getter_named(type, property->name); /* which sites call as returning the property's representation */
    if (getter != NULL ? getter->result != property->check->slot || !implies(s, pts_check_declared(getter->result, getter->check), property->check)
                       : !property->optional) {
      return false;
    }
  }
  return true;
}

/* Whether every value of the object type `sub` has every property of the object type `super` as it is. */
static bool properties_imply(implying *s, const pts_check *sub, const pts_check *super) {
  for (uint32_t i = 0; i < super->count; i++) {
    const pts_check_property *property = &super->properties[i];
    const pts_check_property *own = NULL;
    for (uint32_t j = 0; j < sub->count && own == NULL; j++) {
      if (strcmp(sub->properties[j].name, property->name) == 0) {
        own = &sub->properties[j];
      }
    }
    if (own == NULL ? !property->optional : (own->optional && !property->optional) || !place_implies(s, own->check->slot, own->check, property)) {
      return false;
    }
  }
  return true;
}

/*
 * Whether every value of type sub is a value of type super, held as super holds it where
 * their kinds agree. Arrays, tuples, maps and hash-map objects imply only their own type,
 * since code typed with either may write to them; objects imply the object types whose
 * properties their own declared types imply, and the classes they extend. An optional
 * property that sub doesn't declare is implied too: sub's objects may lack it.
 */
static bool implies(implying *s, const pts_check *sub, const pts_check *super) {
  if (pts_check_same(sub, super) || super->kind == PTS_CHECK_ANY) {
    return true;
  }
  if (sub->kind == PTS_CHECK_UNION) {
    for (uint32_t i = 0; i < sub->count; i++) {
      if (!implies(s, sub->members[i], super)) {
        return false;
      }
    }
    return true;
  }
  switch (super->kind) {
  case PTS_CHECK_UNION:
    for (uint32_t i = 0; i < super->count; i++) {
      if (implies(s, sub, super->members[i])) {
        return true;
      }
    }
    return false;
  case PTS_CHECK_NUMBER:
    return sub->kind == PTS_CHECK_NUMBER || sub->kind == PTS_CHECK_INTEGER; /* as values; places compare their kinds */
  case PTS_CHECK_INSTANCE:
    return sub->kind == PTS_CHECK_INSTANCE && type_extends(sub->type, super->type);
  case PTS_CHECK_OBJECT:
    break;
  default:
    return false;
  }
  if (sub->kind != PTS_CHECK_INSTANCE && sub->kind != PTS_CHECK_OBJECT) {
    return false;
  }
  for (size_t i = 0; i < s->depth; i++) {
    if (s->stack[i].sub == sub && s->stack[i].super == super) {
      return true;
    }
  }
  if (s->depth == MAX_DEPTH) {
    return false;
  }
  s->stack[s->depth].sub = sub;
  s->stack[s->depth].super = super;
  s->depth++;
  bool holds = sub->kind == PTS_CHECK_INSTANCE ? descriptor_implies(s, sub->type, super) : properties_imply(s, sub, super);
  s->depth--;
  return holds;
}

bool pts_check_implies(const pts_check *sub, const pts_check *super) {
  implying s = {.depth = 0};
  return implies(&s, sub, super);
}

/* Whether an object is one of an object type as it is: its descriptor's declared types imply the type's properties. */
static bool object_fits(const pts_object *object, const pts_check *check) {
  implying s = {.depth = 0};
  return descriptor_implies(&s, object->type, check);
}

/* A property of dynamic data to copy: a hash-map object's entry or an object's field or getter; absent if it has none. */
static pts_value source_property(const pts_object *source, const char *name) {
  if (source->type->kind == PTS_KIND_RECORD) {
    pts_string *key = pts_string_from_cstr(name);
    if (!pts_map_has((const pts_map *)source, &key)) {
      return PTS_VALUE_ABSENT;
    }
    return pts_map_get_value((const pts_map *)source, &key);
  }
  const pts_field *field = field_named(source->type, name);
  if (field != NULL) {
    return pts_value_of_slot(field->kind, (const char *)source + field->offset);
  }
  if (getter_named(source->type, name) != NULL) {
    return pts_dynamic_get(pts_value_from_object(source), pts_value_from_string(pts_string_from_cstr(name)));
  }
  return PTS_VALUE_ABSENT;
}

/* Stores value, which fits kind, in a slot of that kind. */
static void store(void *slot, pts_field_kind kind, pts_value value) { pts_store_value_in_slot(kind, slot, value); }

/* A copy of an object or hash-map object as an object of the check's descriptor, each property converted. */
static bool copy_object(converting *c, const pts_object *source, const pts_check *check, pts_value *out) {
  if (check->type == NULL || (source->type->kind != PTS_KIND_OBJECT && source->type->kind != PTS_KIND_RECORD)) {
    return false;
  }
  pts_object *copy = pts_object_new(check->type);
  for (uint32_t i = 0; i < check->count; i++) {
    const pts_check_property *property = &check->properties[i];
    pts_value value = source_property(source, property->name);
    if (value == PTS_VALUE_ABSENT) {
      if (!property->optional) {
        return false;
      }
      continue; /* the copy leaves it out too: its field holds PTS_VALUE_ABSENT */
    }
    pts_value converted;
    if (!convert(c, value, property->check, true, &converted)) {
      return false;
    }
    store((char *)copy + property->offset, property->check->slot, converted);
  }
  *out = pts_value_from_object(copy);
  return true;
}

/* A tuple: one of the check's descriptor, or a copy of an array of as many elements. */
static bool convert_tuple(converting *c, pts_object *object, const pts_check *check, bool copy, pts_value *out) {
  if (object->type == check->type) {
    *out = pts_value_from_object(object);
    return true;
  }
  if (!copy || object->type->kind != PTS_KIND_ARRAY || pts_array_length((const pts_array *)object) != check->count) {
    return false;
  }
  pts_object *tuple = pts_object_new(check->type);
  for (uint32_t i = 0; i < check->count; i++) {
    const pts_check_property *element = &check->properties[i];
    pts_value converted;
    if (!convert(c, pts_array_element((const pts_array *)object, i), element->check, true, &converted)) {
      return false;
    }
    store((char *)tuple + element->offset, element->check->slot, converted);
  }
  *out = pts_value_from_object(tuple);
  return true;
}

/* An array: itself if it is declared with the element type, else a copy with each element converted. */
static bool convert_array(converting *c, pts_array *array, const pts_check *check, bool copy, pts_value *out) {
  const pts_check *element = check->element;
  if (array->type->element == element->slot && pts_check_same(pts_check_declared(array->type->element, array->type->check), element)) {
    *out = pts_value_from_object(array);
    return true;
  }
  if (!copy) {
    return false;
  }
  size_t length = pts_array_length_of(array);
  pts_array *result = pts_array_of(check->type, NULL, 0);
  for (size_t i = 0; i < length; i++) {
    pts_value converted;
    if (!convert(c, pts_array_element(array, (double)i), element, true, &converted)) {
      return false;
    }
    slot_value stored = {0};
    store(&stored, element->slot, converted);
    pts_array_push(result, &stored, 1);
  }
  *out = pts_value_from_object(result);
  return true;
}

/* A hash-map object: itself if it is of the check's descriptor, else a copy of a hash-map object's entries or an object's fields. */
static bool convert_record(converting *c, pts_object *object, const pts_check *check, bool copy, pts_value *out) {
  const pts_check *element = check->element;
  if (object->type == check->type) {
    *out = pts_value_from_object(object);
    return true;
  }
  if (!copy) {
    return false;
  }
  pts_map *result = pts_map_new(check->type);
  if (object->type->kind == PTS_KIND_RECORD) {
    pts_map_iterator entries = pts_record_iterate((pts_map *)object);
    while (pts_map_next(&entries)) {
      pts_value converted;
      if (!convert(c, pts_value_of_slot(((const pts_map *)object)->type->element, &entries.value), element, true, &converted)) {
        return false;
      }
      slot_value stored = {0};
      store(&stored, element->slot, converted);
      pts_map_set(result, &entries.key, &stored);
    }
  } else if (object->type->kind == PTS_KIND_OBJECT) {
    for (uint32_t i = 0; i < object->type->field_count; i++) {
      const pts_field *field = &object->type->fields[i];
      pts_value value = pts_field_is_accessor(field->kind) ? PTS_VALUE_ABSENT : pts_value_of_slot(field->kind, (const char *)object + field->offset);
      if (value == PTS_VALUE_ABSENT) {
        continue;
      }
      pts_value converted;
      if (!convert(c, value, element, true, &converted)) {
        return false;
      }
      slot_value stored = {0};
      store(&stored, element->slot, converted);
      pts_string *key = pts_string_from_cstr(field->name);
      pts_map_set(result, &key, &stored);
    }
  } else {
    return false;
  }
  *out = pts_value_from_object(result);
  return true;
}

static bool convert(converting *c, pts_value value, const pts_check *check, bool copy, pts_value *out) {
  *out = value;
  switch (check->kind) {
  case PTS_CHECK_ANY:
    return true;
  case PTS_CHECK_NUMBER:
    return pts_value_is_number(value) || is_kind(value, PTS_KIND_INT64) || is_kind(value, PTS_KIND_UINT64);
  case PTS_CHECK_INTEGER:
    return pts_value_is_integer_of(value, check->slot);
  case PTS_CHECK_STRING:
    return is_kind(value, PTS_KIND_STRING);
  case PTS_CHECK_BOOLEAN:
    return value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE;
  case PTS_CHECK_NULL:
    return value == PTS_VALUE_NULL;
  case PTS_CHECK_UNDEFINED:
    return value == PTS_VALUE_UNDEFINED;
  case PTS_CHECK_INSTANCE:
    return pts_value_instance_of(value, check->type);
  case PTS_CHECK_UNION:
    for (uint32_t i = 0; i < check->count; i++) {
      if (convert(c, value, check->members[i], false, out)) {
        return true;
      }
    }
    for (uint32_t i = 0; copy && i < check->count; i++) {
      if (convert(c, value, check->members[i], true, out)) {
        return true;
      }
    }
    return false;
  default:
    break;
  }
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_object *object = pts_value_as_object(value);
  if (!enter(c, object, check)) {
    return false; /* a cycle of dynamic data, which a copy can't hold (JSON has none) */
  }
  bool converted = false;
  switch (check->kind) {
  case PTS_CHECK_OBJECT:
    if (object->type->kind == PTS_KIND_OBJECT && object_fits(object, check)) {
      converted = true;
    } else if (copy) {
      converted = copy_object(c, object, check, out);
    }
    break;
  case PTS_CHECK_TUPLE:
    converted = convert_tuple(c, object, check, copy, out);
    break;
  case PTS_CHECK_ARRAY:
    converted = object->type->kind == PTS_KIND_ARRAY && convert_array(c, (pts_array *)object, check, copy, out);
    break;
  case PTS_CHECK_RECORD:
    converted = convert_record(c, object, check, copy, out);
    break;
  case PTS_CHECK_MAP:
  case PTS_CHECK_SET:
    converted = object->type == check->type; /* the same key and value types: maps aren't copied */
    break;
  default:
    break;
  }
  c->depth--;
  return converted;
}

bool pts_value_try_convert(pts_value value, const pts_check *check, pts_value *out) {
  converting c = {.depth = 0};
  return convert(&c, value, check, true, out);
}

pts_value pts_value_convert(pts_value value, const pts_check *check) {
  pts_value result;
  if (!pts_value_try_convert(value, check, &result)) {
    pts_throw_type_mismatch(value, check->name);
  }
  return result;
}
