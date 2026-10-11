/*
 * Converting dynamic values to the program's types (D-071). A value that already is one
 * of the target type, as its representation goes, is returned as it is, so a typed object
 * that went through `any` keeps its identity; dynamic data (an `any[]`, a hash-map object
 * of `any`, parsed JSON) is checked and copied into the target's representation, since an
 * `any[]` holds pts_values where a `number[]` holds doubles.
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

/* Whether (object, check) is being converted already: a cycle, which fits if nothing is copied (coinduction). */
static bool enter(converting *c, const pts_object *object, const pts_check *check, bool *cycle) {
  *cycle = false;
  for (size_t i = 0; i < c->depth; i++) {
    if (c->stack[i].object == object && c->stack[i].check == check) {
      *cycle = true;
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

/* Whether a field of an object fits a property of the target as it is: its kind is the property's, and an object or dynamic value in it fits too. */
static bool field_fits(converting *c, const pts_object *object, const pts_field *field, const pts_check_property *property) {
  const void *slot = (const char *)object + field->offset;
  pts_value value = pts_value_of_slot(field->kind, slot);
  if (field->kind != property->check->slot) {
    /* An optional property may be held as its type without undefined (D-066), which the property's sites convert. */
    pts_value ignored;
    return property->optional && field->kind != PTS_FIELD_OBJECT && field->kind != PTS_FIELD_VALUE &&
           convert(c, value, property->check, false, &ignored);
  }
  if (field->kind != PTS_FIELD_OBJECT && field->kind != PTS_FIELD_VALUE) {
    return true; /* a number field holds numbers, and so on */
  }
  if (value == PTS_VALUE_ABSENT) {
    return property->optional;
  }
  pts_value same;
  return convert(c, value, property->check, false, &same);
}

/* Whether an object fits an object type as it is: each property is a field that fits, a getter of its kind, or missing and optional. */
static bool object_fits(converting *c, const pts_object *object, const pts_check *check) {
  for (uint32_t i = 0; i < check->count; i++) {
    const pts_check_property *property = &check->properties[i];
    const pts_field *field = field_named(object->type, property->name);
    if (field != NULL) {
      if (!field_fits(c, object, field, property)) {
        return false;
      }
      continue;
    }
    const pts_method *getter = getter_named(object->type, property->name);
    if (getter != NULL ? getter->result != property->check->slot || getter->result == PTS_FIELD_OBJECT : !property->optional) {
      return false;
    }
  }
  return true;
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

/* A tuple: one of the check's descriptor whose elements fit, or a copy of an array of as many elements. */
static bool convert_tuple(converting *c, pts_object *object, const pts_check *check, bool copy, pts_value *out) {
  if (object->type == check->type) {
    for (uint32_t i = 0; i < check->count; i++) {
      const pts_check_property *element = &check->properties[i];
      if (element->check->slot == PTS_FIELD_OBJECT || element->check->slot == PTS_FIELD_VALUE) {
        pts_value same;
        if (!convert(c, pts_value_of_slot(element->check->slot, (const char *)object + element->offset), element->check, false, &same)) {
          return false;
        }
      }
    }
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

/* An array: itself if its elements are of the element type's kind and fit, else a copy with each element converted. */
static bool convert_array(converting *c, pts_array *array, const pts_check *check, bool copy, pts_value *out) {
  const pts_check *element = check->element;
  size_t length = pts_array_length_of(array);
  bool fits = array->type->element == element->slot;
  for (size_t i = 0; fits && i < length && (element->slot == PTS_FIELD_OBJECT || element->slot == PTS_FIELD_VALUE); i++) {
    pts_value same;
    fits = convert(c, pts_array_element(array, (double)i), element, false, &same);
  }
  if (fits) {
    *out = pts_value_from_object(array);
    return true;
  }
  if (!copy) {
    return false;
  }
  pts_array *result = pts_array_of(element->slot, NULL, 0);
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

/* A hash-map object: itself if it is of the check's descriptor and its values fit, else a copy of a hash-map object's entries or an object's fields. */
static bool convert_record(converting *c, pts_object *object, const pts_check *check, bool copy, pts_value *out) {
  const pts_check *element = check->element;
  if (object->type == check->type) {
    bool fits = true;
    if (element->slot == PTS_FIELD_OBJECT || element->slot == PTS_FIELD_VALUE) {
      pts_map_iterator entries = pts_record_iterate((pts_map *)object);
      while (fits && pts_map_next(&entries)) {
        pts_value same;
        fits = convert(c, pts_value_of_slot(element->slot, &entries.value), element, false, &same);
      }
    }
    if (fits) {
      *out = pts_value_from_object(object);
      return true;
    }
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
  bool cycle;
  if (!enter(c, object, check, &cycle)) {
    return cycle && !copy; /* a cycle fits as it is; one that would have to be copied doesn't */
  }
  bool converted = false;
  switch (check->kind) {
  case PTS_CHECK_OBJECT:
    if (object->type->kind == PTS_KIND_OBJECT && object_fits(c, object, check)) {
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

pts_value pts_value_convert(pts_value value, const pts_check *check) {
  converting c = {.depth = 0};
  pts_value result;
  if (!convert(&c, value, check, true, &result)) {
    pts_throw_type_mismatch(value, check->name);
  }
  return result;
}
