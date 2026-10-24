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
#include "dynamic_internal.h"

#include <stdio.h>
#include <stdlib.h>

enum { MAX_DEPTH = 256 };

/* The objects being converted, with their targets: meeting one again is a cycle. */
typedef struct converting {
  struct {
    const pts_object *object;
    const pts_check *check;
  } stack[MAX_DEPTH];
  size_t depth;
  /*
   * Set for a union's member tried before the union's iterator results: a result's check
   * then takes only objects of its own layout, so that another object member gets the
   * object first, as it is or as a copy (D-178). convert clears it for the levels below.
   */
  bool results_last;
  /* Set for a read that converts only as it is (pts_value_try_convert_as_is): a closure of another type is still adapted, since an adapter holds no data a write could change. */
  bool adapt;
} converting;

static bool convert(converting *c, pts_value value, const pts_check *check, bool copy, pts_value *out);
static void store(void *slot, pts_field_kind kind, pts_value value);
static bool descriptor_resembles(const pts_type *type, const pts_check *target, int depth);

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
static bool descriptor_has_properties(implying *s, const pts_type *type, const pts_check *super, bool readable);
static bool readable_as(implying *s, const pts_check *declared, const pts_check *target);

/* A hash of two pointers to static data, for the per-thread caches below: its top bits pick an entry. */
static uint64_t pair_hash(const void *a, const void *b) { return ((uint64_t)(uintptr_t)a ^ (uint64_t)(uintptr_t)b << 1) * UINT64_C(0x9E3779B97F4A7C15); }

/* ---------------------------------------------------------------- members of unions of object types */

/* Whether an object type's check declares a property name: one of its properties, or a field or getter of its class's descriptor. */
static bool check_declares(const pts_check *check, const char *name) {
  if (check->kind == PTS_CHECK_INSTANCE) {
    return field_named(check->type, name) != NULL || getter_named(check->type, name) != NULL;
  }
  if (check->kind != PTS_CHECK_OBJECT) {
    return false;
  }
  for (uint32_t i = 0; i < check->count; i++) {
    if (strcmp(check->properties[i].name, name) == 0) {
      return true;
    }
  }
  return false;
}

/*
 * The properties that the object types among a union's members declare, each once, with
 * the members that declare each (bits; a member past the 64th declares every one). One
 * per union check, made when a conversion first meets it and kept for the program's life
 * (checks are static data); a table of them is shared by all threads.
 */
typedef struct union_properties {
  const pts_check *u;
  uint32_t count;
  struct {
    const char *name;
    size_t length;
    uint64_t members;
  } names[64];
} union_properties;

enum { UNION_TABLE_SIZE = 1024, UNION_TABLE_PROBES = 16 };
static union_properties *union_table[UNION_TABLE_SIZE];

/* Fills p with u's properties; count stays 0 unless two or more of u's members are object types, which is when members differ in them. */
static void collect_union_properties(const pts_check *u, union_properties *p) {
  p->u = u;
  p->count = 0;
  uint32_t objects = 0;
  for (uint32_t i = 0; i < u->count; i++) {
    objects += u->members[i]->kind == PTS_CHECK_OBJECT;
  }
  if (objects < 2) {
    return;
  }
  for (uint32_t i = 0; i < u->count; i++) {
    const pts_check *member = u->members[i];
    for (uint32_t j = 0; member->kind == PTS_CHECK_OBJECT && j < member->count && p->count < 64; j++) {
      const char *name = member->properties[j].name;
      bool known = false;
      for (uint32_t k = 0; k < p->count && !known; k++) {
        known = strcmp(p->names[k].name, name) == 0;
      }
      if (known) {
        continue;
      }
      uint64_t members = UINT64_MAX;
      for (uint32_t m = 0; m < u->count && m < 64; m++) {
        if (!check_declares(u->members[m], name)) {
          members &= ~(UINT64_C(1) << m);
        }
      }
      p->names[p->count].name = name;
      p->names[p->count].length = strlen(name);
      p->names[p->count].members = members;
      p->count++;
    }
  }
}

/* u's properties, from the table, else made into it (or into *spare if its slots near u's are full). */
static const union_properties *properties_of(const pts_check *u, union_properties *spare) {
  size_t slot = ((uintptr_t)u >> 4) % UNION_TABLE_SIZE;
  for (size_t probe = 0; probe < UNION_TABLE_PROBES; probe++, slot = (slot + 1) % UNION_TABLE_SIZE) {
    union_properties *found = __atomic_load_n(&union_table[slot], __ATOMIC_ACQUIRE);
    if (found == NULL) {
      union_properties *made = malloc(sizeof(union_properties));
      if (made == NULL) {
        break;
      }
      collect_union_properties(u, made);
      if (__atomic_compare_exchange_n(&union_table[slot], &found, made, false, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) {
        return made;
      }
      free(made); /* another thread filled the slot first: found is its */
    }
    if (found->u == u) {
      return found;
    }
  }
  collect_union_properties(u, spare);
  return spare;
}

/*
 * Which members of the union u take a value or type `thing` (as bits; a member past the
 * 64th always does): those that declare every property that an object type among u's
 * members declares and that has(thing, name) says thing has. tsc narrows a value of a
 * union of object types to the members its discriminants or `in` select, which the IR's
 * types can't tell apart (tags are strings), so a value is read as any member that
 * declares a property it has: a `{ tag: "some", value: any[] }` that an erased copy made
 * fits `{ tag: "none" }` as it is, and code narrowed by its tag reads its value as the
 * other member's (C187). A union takes such a value as a member that leaves out none of
 * its properties, as it is or by a copy, before any other (D-192).
 */
static uint64_t covering(const pts_check *u, bool (*has)(const void *thing, const char *name), const void *thing) {
  union_properties spare;
  const union_properties *p = properties_of(u, &spare);
  uint64_t members = UINT64_MAX;
  for (uint32_t i = 0; i < p->count; i++) {
    if (has(thing, p->names[i].name)) {
      members &= p->names[i].members;
    }
  }
  return members;
}

/* covering of a hash-map object, by its entries now: its keys are compared with u's properties one by one, since parsed objects have few. */
static uint64_t covering_record(const pts_check *u, const pts_map *record) {
  union_properties spare;
  const union_properties *p = properties_of(u, &spare);
  uint64_t members = UINT64_MAX;
  if (p->count == 0) {
    return members;
  }
  pts_map_iterator entries = pts_map_iterate((pts_map *)record);
  while (pts_map_next(&entries)) {
    const pts_string *key = PTS_MAP_ITERATOR_KEY(const pts_string *, entries);
    for (uint32_t i = 0; i < p->count; i++) {
      if (key->byte_len == p->names[i].length && memcmp(key->bytes, p->names[i].name, key->byte_len) == 0) {
        members &= p->names[i].members;
        break;
      }
    }
  }
  return members;
}

/*
 * Whether a place declared `declared` holds values of a type that `target` is, up to `any`:
 * the type an erased copy wrote for target, with dynamic values where target has its type
 * arguments (`{ n: any }` for `{ n: number }`, `any[]` for `number[]`). A copy of a value
 * into a union goes first to a member its declared types resemble so (D-192), since tags
 * are strings in the IR and several members may convert it: an erased copy's `{ tag:
 * "nested", value: { n: any } }` converts to a `{ tag: "keyed"; value: Record<string, T> }`
 * as well. Recursive types resemble at the depth limit.
 */
static bool resembles(const pts_check *declared, const pts_check *target, int depth) {
  if (declared == target || declared->kind == PTS_CHECK_ANY || target->kind == PTS_CHECK_ANY || depth == 0) {
    return true;
  }
  switch (target->kind) {
  case PTS_CHECK_UNION:
    for (uint32_t i = 0; i < target->count; i++) {
      if (resembles(declared, target->members[i], depth - 1)) {
        return true;
      }
    }
    return false;
  case PTS_CHECK_NUMBER:
  case PTS_CHECK_INTEGER:
    return declared->kind == PTS_CHECK_NUMBER || declared->kind == PTS_CHECK_INTEGER;
  case PTS_CHECK_ARRAY:
  case PTS_CHECK_RECORD:
    return declared->kind == target->kind && (declared->element == NULL || target->element == NULL || resembles(declared->element, target->element, depth - 1));
  case PTS_CHECK_TUPLE:
    if (declared->kind != PTS_CHECK_TUPLE || declared->count != target->count) {
      return false;
    }
    for (uint32_t i = 0; i < target->count; i++) {
      if (!resembles(declared->properties[i].check, target->properties[i].check, depth - 1)) {
        return false;
      }
    }
    return true;
  case PTS_CHECK_OBJECT:
    if (declared->kind == PTS_CHECK_INSTANCE) {
      return descriptor_resembles(declared->type, target, depth - 1);
    }
    if (declared->kind != PTS_CHECK_OBJECT) {
      return false;
    }
    for (uint32_t i = 0; i < target->count; i++) {
      const pts_check_property *property = &target->properties[i], *own = NULL;
      for (uint32_t j = 0; j < declared->count && own == NULL; j++) {
        if (strcmp(declared->properties[j].name, property->name) == 0) {
          own = &declared->properties[j];
        }
      }
      if (own == NULL ? !property->optional : !resembles(own->check, property->check, depth - 1)) {
        return false;
      }
    }
    return true;
  default:
    return declared->kind == target->kind && (target->kind != PTS_CHECK_INSTANCE || declared->type == target->type);
  }
}

/* Whether objects of a descriptor hold every property of the object type target as a type that its type resembles (resembles). */
static bool descriptor_resembles(const pts_type *type, const pts_check *target, int depth) {
  for (uint32_t i = 0; i < target->count; i++) {
    const pts_check_property *property = &target->properties[i];
    const pts_field *field = field_named(type, property->name);
    const pts_method *getter = field == NULL ? getter_named(type, property->name) : NULL;
    if (field != NULL ? !resembles(pts_check_declared(field->kind, field->check), property->check, depth)
                      : getter != NULL ? !resembles(pts_check_declared(getter->result, getter->check), property->check, depth) : !property->optional) {
      return false;
    }
  }
  return true;
}

/* Which members of u objects of a descriptor resemble (descriptor_resembles), as covering's bits. */
static uint64_t resembling(const pts_check *u, const pts_type *type) {
  uint64_t members = UINT64_MAX;
  for (uint32_t i = 0; i < u->count && i < 64; i++) {
    if (u->members[i]->kind != PTS_CHECK_OBJECT || !descriptor_resembles(type, u->members[i], 8)) {
      members &= ~(UINT64_C(1) << i);
    }
  }
  return members;
}

/* Which object members of u (as covering's bits; none past the 64th) sites read objects of a descriptor as, converted as they are (object_readable). */
static uint64_t readable_members(const pts_check *u, const pts_type *type) {
  uint64_t members = 0;
  for (uint32_t i = 0; i < u->count && i < 64; i++) {
    implying s; /* not zero-filled, as in pts_check_implies */
    s.depth = 0;
    if (u->members[i]->kind == PTS_CHECK_OBJECT && !u->members[i]->result && descriptor_has_properties(&s, type, u->members[i], true)) {
      members |= UINT64_C(1) << i;
    }
  }
  return members;
}

/* Whether a member of a union is one that covering's bits name. */
static bool covers(uint64_t bits, uint32_t member) { return member >= 64 || (bits >> member & 1) != 0; }

/* Whether a type's check declares the property name (covering's has, for types). */
static bool type_has(const void *check, const char *name) { return check_declares(check, name); }

/* Whether a descriptor of objects declares the property name: a field or a getter (covering's has, for objects). */
static bool descriptor_has(const void *type, const char *name) { return field_named(type, name) != NULL || getter_named(type, name) != NULL; }

/*
 * The members of u that take objects of a descriptor or values of a type (covering), per
 * thread: both are static data and the answer never changes, and a conversion of an
 * object to a union would otherwise search their properties by name each time.
 */
typedef struct covering_entry {
  const pts_check *u;
  const void *thing;
  bool (*has)(const void *thing, const char *name);
  uint64_t members, resembling, readable; /* for a descriptor: the members its objects resemble, and those whose sites read them (object_readable) */
} covering_entry;
enum { COVERING_SETS = 256, COVERING_WAYS = 4 };
static _Thread_local covering_entry covering_cache[COVERING_SETS * COVERING_WAYS];

static const covering_entry *covering_cached(const pts_check *u, bool (*has)(const void *thing, const char *name), const void *thing) {
  covering_entry *set = &covering_cache[(pair_hash(u, thing) >> (64 - 8)) * COVERING_WAYS]; /* the last used first */
  covering_entry found;
  int way = 0;
  while (way < COVERING_WAYS && !(set[way].u == u && set[way].thing == thing && set[way].has == has)) {
    way++;
  }
  if (way == 0) {
    return &set[0];
  }
  if (way < COVERING_WAYS) {
    found = set[way];
  } else {
    bool descriptor = has == descriptor_has;
    found = (covering_entry){u, thing, has, covering(u, has, thing), descriptor ? resembling(u, thing) : UINT64_MAX, descriptor ? readable_members(u, thing) : 0};
    way = COVERING_WAYS - 1;
  }
  memmove(&set[1], &set[0], (size_t)way * sizeof(covering_entry));
  set[0] = found;
  return &set[0];
}

/*
 * covering of an object or hash-map object (by its descriptor's fields and getters, or a
 * hash-map object's entries now), in *resemble the members an object resembles (a hash-map
 * object's entries have no declared types to tell), and in *readable the object members
 * whose sites read an object's properties as they are (none for a hash-map object).
 */
static uint64_t covering_value(const pts_check *u, const pts_object *object, uint64_t *resemble, uint64_t *readable) {
  *resemble = UINT64_MAX;
  *readable = 0;
  switch (object->type->kind) {
  case PTS_KIND_OBJECT: {
    const covering_entry *entry = covering_cached(u, descriptor_has, pts_unfrozen(object->type));
    *resemble = entry->resembling;
    *readable = entry->readable;
    return entry->members;
  }
  case PTS_KIND_RECORD:
    return covering_record(u, (const pts_map *)object);
  default:
    return UINT64_MAX;
  }
}

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

/* The method table entry of a method named name, or NULL. */
static const pts_method *method_named(const pts_type *type, const char *name) {
  for (uint32_t i = 0; i < type->method_count; i++) {
    if (type->methods[i].kind == PTS_METHOD && strcmp(type->methods[i].name, name) == 0) {
      return &type->methods[i];
    }
  }
  return NULL;
}

/* Whether every object of descriptor `type` has every property and method of the object type `super` as it is (by its declared types). */
/*
 * Whether a place of kind `kind` declared `declared` holds only values that a site of a
 * property's type reads as they are, converted (object.c's read_converted, D-192): the
 * values of a type that implies the property's in another representation (a number where
 * `number | string` goes), or closures, which a read adapts (D-075).
 */
static bool place_readable(implying *s, pts_field_kind kind, const pts_check *declared, const pts_check_property *property) {
  return place_implies(s, kind, declared, property) || readable_as(s, declared, property->check);
}

/*
 * Whether every value of type `declared` is one that a read of type `target` takes as it is,
 * converted (pts_value_try_convert_as_is): a value of a type that implies target's, a
 * closure where a function type goes (adapted), possibly among a union's members (an
 * optional callback), or an object whose properties are read so (nested object types).
 */
static bool readable_as(implying *s, const pts_check *declared, const pts_check *target) {
  if (implies(s, declared, target)) {
    return true;
  }
  if (declared->kind == PTS_CHECK_UNION) { /* each of its members: an optional callback's closure or undefined */
    for (uint32_t i = 0; i < declared->count; i++) {
      if (!readable_as(s, declared->members[i], target)) {
        return false;
      }
    }
    return true;
  }
  switch (target->kind) {
  case PTS_CHECK_FUNCTION:
    return declared->kind == PTS_CHECK_FUNCTION && target->adapt != NULL;
  case PTS_CHECK_UNION:
    for (uint32_t i = 0; i < target->count; i++) {
      if (readable_as(s, declared, target->members[i])) {
        return true;
      }
    }
    return false;
  case PTS_CHECK_OBJECT:
    break;
  default:
    return false;
  }
  if (target->result || s->depth == MAX_DEPTH || (declared->kind != PTS_CHECK_OBJECT && declared->kind != PTS_CHECK_INSTANCE)) {
    return false;
  }
  for (size_t i = 0; i < s->depth; i++) {
    if (s->stack[i].sub == declared && s->stack[i].super == target) {
      return true; /* recursive types, coinductively, as implies */
    }
  }
  s->stack[s->depth].sub = declared;
  s->stack[s->depth].super = target;
  s->depth++;
  bool readable;
  if (declared->kind == PTS_CHECK_INSTANCE) {
    readable = descriptor_has_properties(s, declared->type, target, true);
  } else {
    readable = target->method_count == 0;
    for (uint32_t i = 0; readable && i < target->count; i++) {
      const pts_check_property *property = &target->properties[i], *own = NULL;
      for (uint32_t j = 0; j < declared->count && own == NULL; j++) {
        if (strcmp(declared->properties[j].name, property->name) == 0) {
          own = &declared->properties[j];
        }
      }
      readable = own == NULL ? property->optional : (!own->optional || property->optional) && place_readable(s, own->check->slot, own->check, property);
    }
  }
  s->depth--;
  return readable;
}

/*
 * Whether every object of descriptor `type` has every property and method of the object
 * type `super` as it is (by its declared types), or for readable, has them as sites of
 * super's types read them, converted as they are (place_readable).
 */
static bool descriptor_has_properties(implying *s, const pts_type *type, const pts_check *super, bool readable) {
  for (uint32_t i = 0; i < super->method_count; i++) {
    const pts_method *method = method_named(type, super->methods[i].name);
    if (method == NULL || (method->signature != super->methods[i].signature && method->dynamic == NULL)) {
      return false; /* interfaces call a method of another function type through its dynamic entry (D-078) */
    }
  }
  for (uint32_t i = 0; i < super->count; i++) {
    const pts_check_property *property = &super->properties[i];
    const pts_field *field = field_named(type, property->name);
    if (field != NULL) {
      const pts_check *declared = pts_check_declared(field->kind, field->check);
      if (readable ? !place_readable(s, field->kind, declared, property) : !place_implies(s, field->kind, declared, property)) {
        return false;
      }
      continue;
    }
    const pts_method *getter = getter_named(type, property->name); /* which sites call as returning the property's representation */
    if (getter != NULL ? (!readable && getter->result != property->check->slot) || !implies(s, pts_check_declared(getter->result, getter->check), property->check)
                       : !property->optional) {
      return false;
    }
  }
  return true;
}

static bool descriptor_implies(implying *s, const pts_type *type, const pts_check *super) { return descriptor_has_properties(s, type, super, false); }

/* Whether every value of the object type `sub` has every property and method of the object type `super` as it is. */
static bool properties_imply(implying *s, const pts_check *sub, const pts_check *super) {
  for (uint32_t i = 0; i < super->method_count; i++) {
    bool found = false;
    for (uint32_t j = 0; j < sub->method_count && !found; j++) {
      found = strcmp(sub->methods[j].name, super->methods[i].name) == 0 && sub->methods[j].signature == super->methods[i].signature;
    }
    if (!found) {
      return false;
    }
  }
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
  if (sub->result && super->kind == PTS_CHECK_OBJECT && !super->result) {
    return false; /* an iterator result's object may be of any layout (D-178): the declared types say nothing of it */
  }
  switch (super->kind) {
  case PTS_CHECK_UNION: {
    uint64_t members = covering_cached(super, type_has, sub)->members; /* sub's values may be read as any member declaring a property sub declares */
    for (uint32_t i = 0; i < super->count; i++) {
      if (covers(members, i) && implies(s, sub, super->members[i])) {
        return true;
      }
    }
    return false;
  }
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

/*
 * Implications decided, per thread: checks are static data, so the answer for a pair never
 * changes, and a read site that keeps missing its cache on objects whose declared types
 * differ from its own would otherwise decide it at each miss (D-192).
 */
typedef struct implies_entry {
  const pts_check *sub, *super;
  bool holds;
} implies_entry;
static _Thread_local implies_entry implies_cache[256];

bool pts_check_implies(const pts_check *sub, const pts_check *super) {
  if (pts_check_same(sub, super)) {
    return true;
  }
  implies_entry *entry = &implies_cache[pair_hash(sub, super) >> (64 - 8)];
  if (entry->sub != sub || entry->super != super) {
    implying s; /* not zero-filled: only the entries below depth are read, and the stack is 4 KB */
    s.depth = 0;
    *entry = (implies_entry){sub, super, implies(&s, sub, super)};
  }
  return entry->holds;
}

/* Whether an object is one of an object type as it is: its descriptor's declared types imply the type's properties. */
static bool object_fits(const pts_object *object, const pts_check *check) {
  implying s; /* not zero-filled, as in pts_check_implies */
  s.depth = 0;
  return descriptor_implies(&s, object->type, check);
}

/* Whether sites of an object type read an object's properties as they are, converted (descriptor_has_properties). */
static bool object_readable(const pts_object *object, const pts_check *check) {
  implying s; /* not zero-filled, as in pts_check_implies */
  s.depth = 0;
  return check->kind == PTS_CHECK_OBJECT && !check->result && descriptor_has_properties(&s, pts_unfrozen(object->type), check, true);
}

/*
 * Whether an object is a class's instance (or an error's), whose class and methods a copy
 * into an object type's shape would lose: descriptors of object literals' shapes are named
 * "Object", as console.log's are (inspect.c).
 */
static bool is_class_instance(const pts_object *object) {
  return object->type->kind == PTS_KIND_OBJECT && strcmp(pts_unfrozen(object->type)->name, "Object") != 0;
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
  if (field != NULL && field->kind == PTS_FIELD_VALUE) { /* an optional property the object lacks stays absent in the copy (D-192) */
    return __atomic_load_n((const pts_value *)((const char *)source + field->offset), __ATOMIC_RELAXED);
  }
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
  if (check->type == NULL || check->method_count > 0 || (source->type->kind != PTS_KIND_OBJECT && source->type->kind != PTS_KIND_RECORD)) {
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

/*
 * A tuple: one of the check's descriptor, or a copy of an array or of another tuple (an
 * erased copy's `[string, any]` for a `[string, number]`, D-078) of as many elements, or
 * fewer by optional ones, which the copy leaves out (D-100).
 */
static bool convert_tuple(converting *c, pts_object *object, const pts_check *check, bool copy, pts_value *out) {
  if (pts_unfrozen(object->type) == check->type) { /* a frozen one too, which a copy would thaw (D-157) */
    *out = pts_value_from_object(object);
    return true;
  }
  bool array = object->type->kind == PTS_KIND_ARRAY;
  if (!copy || (!array && object->type->kind != PTS_KIND_TUPLE)) {
    return false;
  }
  double length = array ? pts_array_length((const pts_array *)object) : (double)pts_tuple_length(object);
  if (length > check->count || (length < check->count && !check->properties[(uint32_t)length].optional)) {
    return false; /* optional elements come last, so the first one left out is optional or none is */
  }
  pts_object *tuple = pts_object_new(check->type);
  for (uint32_t i = 0; i < (uint32_t)length; i++) {
    const pts_check_property *element = &check->properties[i];
    pts_value value;
    if (array) {
      value = pts_array_element((const pts_array *)object, i);
    } else {
      const pts_field *field = &object->type->fields[i]; /* a tuple's fields are its elements, in order */
      value = pts_value_of_slot(field->kind, (const char *)object + field->offset);
    }
    pts_value converted;
    if (!convert(c, value, element->check, true, &converted)) {
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

/* A tuple, which is an array in JS: a copy with each element converted, as an array of other elements is copied (C70, D-190). */
static bool copy_tuple_into_array(converting *c, pts_object *tuple, const pts_check *check, pts_value *out) {
  const pts_check *element = check->element;
  size_t length = pts_tuple_length(tuple);
  pts_array *result = pts_array_of(check->type, NULL, 0);
  for (size_t i = 0; i < length; i++) {
    const pts_field *field = &tuple->type->fields[i]; /* a tuple's fields are its elements, in order */
    pts_value converted;
    if (!convert(c, pts_value_of_slot(field->kind, (const char *)tuple + field->offset), element, true, &converted)) {
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
  if (pts_unfrozen(object->type) == check->type) {
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

/*
 * A map or set: itself if it is of the check's descriptor (the same key and value types),
 * else a copy of a map or set of other types (an erased copy's `Map<string, any>` for a
 * `Map<string, number>`, C187) with each value converted and each key as it is, in its
 * order: a copied key would no longer find what the program looks up by it (D-192).
 */
static bool convert_map(converting *c, pts_object *object, const pts_check *check, bool copy, pts_value *out) {
  const pts_type *target = check->type;
  if (pts_unfrozen(object->type) == target) {
    *out = pts_value_from_object(object);
    return true;
  }
  if (!copy || object->type->kind != target->kind) {
    return false;
  }
  const pts_map *source = (const pts_map *)object;
  const pts_check *key = pts_check_declared(target->key, target->key_check);
  const pts_check *element = target->kind == PTS_KIND_MAP ? pts_check_declared(target->element, check->element) : NULL;
  pts_map *result = pts_map_new(target);
  pts_map_iterator entries = pts_map_iterate((pts_map *)source);
  while (pts_map_next(&entries)) {
    pts_value converted;
    if (!convert(c, pts_value_of_slot(source->type->key, &entries.key), key, false, &converted)) {
      return false;
    }
    slot_value stored_key = {0};
    store(&stored_key, target->key, converted);
    if (element == NULL) {
      pts_map_set(result, &stored_key, NULL);
      continue;
    }
    if (!convert(c, pts_value_of_slot(source->type->element, &entries.value), element, true, &converted)) {
      return false;
    }
    slot_value stored = {0};
    store(&stored, target->element, converted);
    pts_map_set(result, &stored_key, &stored);
  }
  *out = pts_value_from_object(result);
  return true;
}

/* A member of the union check that covers value's properties (covering), or for !covering one that doesn't, which takes value as it is. */
static bool union_member_as_is(converting *c, pts_value value, const pts_check *check, uint64_t members, bool covering, pts_value *out) {
  for (uint32_t i = 0; i < check->count; i++) {
    if (covers(members, i) == covering) {
      c->results_last = true;
      if (convert(c, value, check->members[i], false, out)) {
        return true;
      }
    }
  }
  return false;
}

/* union_member_as_is by a copy, into the members value's declared types resemble first (resembles); never an iterator result. */
static bool union_member_copy(converting *c, pts_value value, const pts_check *check, uint64_t members, uint64_t resemble, bool covering, pts_value *out) {
  for (int resembling = 1; resembling >= 0; resembling--) {
    for (uint32_t i = 0; i < check->count; i++) {
      if (!check->members[i]->result && covers(members, i) == covering && covers(resemble, i) == (resembling == 1)) {
        c->results_last = true;
        if (convert(c, value, check->members[i], true, out)) {
          return true;
        }
      }
    }
    if (resemble == UINT64_MAX) {
      break;
    }
  }
  return false;
}

static bool convert(converting *c, pts_value value, const pts_check *check, bool copy, pts_value *out) {
  *out = value;
  bool results_last = c->results_last;
  c->results_last = false;
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
  case PTS_CHECK_FUNCTION: {
    if (!is_kind(value, PTS_KIND_FUNCTION)) {
      return false;
    }
    pts_closure *closure = (pts_closure *)pts_value_as_object(value);
    if (closure->type->signature == check) {
      return true;
    }
    if ((!copy && !c->adapt) || closure->type->call == NULL || check->adapt == NULL) {
      return false;
    }
    *out = pts_value_from_object(check->adapt(closure)); /* a closure of another type, called through its dynamic entry */
    return true;
  }
  case PTS_CHECK_UNION: {
    /*
     * The members that cover the object's properties (covering) come first: as it is, as
     * its properties can be read (object_readable), then by a copy (into the members its
     * declared types resemble first); then the others, as it is, then by a copy. A class's
     * instance is copied into a member's shape only if no member takes it as it is.
     */
    if (pts_value_is_object(value)) {
      const pts_type *type = pts_unfrozen(pts_value_as_object(value)->type);
      for (uint32_t i = 0; i < check->count; i++) {
        if (check->members[i]->kind == PTS_CHECK_OBJECT && check->members[i]->type == type && !check->members[i]->result) {
          return true; /* an object of a member's own descriptor, which declares just that member's properties */
        }
      }
    }
    const pts_object *object = pts_value_is_object(value) ? pts_value_as_object(value) : NULL;
    uint64_t resemble = UINT64_MAX, readable = 0;
    uint64_t members = object != NULL ? covering_value(check, object, &resemble, &readable) : UINT64_MAX;
    int passes = members == UINT64_MAX ? 1 : 2;
    bool instance = object != NULL && is_class_instance(object); /* copied only if no member takes it as it is */
    for (int pass = 0; pass < passes; pass++) {
      if (union_member_as_is(c, value, check, members, pass == 0, out)) {
        return true;
      }
      for (uint32_t i = 0; pass == 0 && readable != 0 && i < check->count; i++) {
        if (covers(members, i) && (i >= 64 ? object_readable(object, check->members[i]) : (readable >> i & 1) != 0)) {
          return true; /* one whose properties its sites read as they are, converted (D-192) */
        }
      }
      if (copy && !instance && union_member_copy(c, value, check, members, resemble, pass == 0, out)) {
        return true;
      }
    }
    for (int pass = 0; copy && instance && pass < passes; pass++) {
      if (union_member_copy(c, value, check, members, resemble, pass == 0, out)) {
        return true;
      }
    }
    for (uint32_t i = 0; i < check->count; i++) { /* then an iterator result takes any of the program's objects */
      if (!check->members[i]->result) {
        continue;
      }
      c->results_last = false;
      if (convert(c, value, check->members[i], copy, out)) {
        return true;
      }
    }
    return false;
  }
  default:
    break;
  }
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_object *object = pts_value_as_object(value);
  if (check->kind == PTS_CHECK_OBJECT && check->type != NULL && pts_unfrozen(object->type) == check->type) {
    return true; /* an object of the check's own descriptor, as a checked narrowed read finds (D-140) */
  }
  if (check->result && !results_last && object->type->kind == PTS_KIND_OBJECT) {
    return true; /* an iterator result, as an iterator handed it back: nothing of it is read (D-178) */
  }
  if (!enter(c, object, check)) {
    return false; /* a cycle of dynamic data, which a copy can't hold (JSON has none) */
  }
  bool converted = false;
  switch (check->kind) {
  case PTS_CHECK_OBJECT:
    if (object->type->kind == PTS_KIND_OBJECT && (c->adapt || object_fits(object, check))) {
      /*
       * As it is. A read (adapt) takes any object as an object type: its properties are only
       * ever read through sites, which verify each one's declared type as it is read (D-192),
       * so a property it reads as another type need not hold for the others.
       */
      converted = true;
    } else if (copy) {
      converted = copy_object(c, object, check, out);
    }
    break;
  case PTS_CHECK_TUPLE:
    converted = convert_tuple(c, object, check, copy, out);
    break;
  case PTS_CHECK_ARRAY:
    if (object->type->kind == PTS_KIND_ARRAY) {
      converted = convert_array(c, (pts_array *)object, check, copy, out);
    } else if (object->type->kind == PTS_KIND_TUPLE && copy) {
      converted = copy_tuple_into_array(c, object, check, out);
    }
    break;
  case PTS_CHECK_RECORD:
    converted = convert_record(c, object, check, copy, out);
    break;
  case PTS_CHECK_MAP:
  case PTS_CHECK_SET:
    converted = convert_map(c, object, check, copy, out);
    break;
  default:
    break;
  }
  c->depth--;
  if (converted && *out != value && pts_value_is_frozen(value)) {
    pts_value_freeze(*out); /* a copy of a frozen object (or a marked map, D-105) is frozen too, as the object JS keeps is (D-157, D-192) */
  }
  return converted;
}

bool pts_value_try_convert(pts_value value, const pts_check *check, pts_value *out) {
  converting c; /* not zero-filled: only the entries below depth are read, and a 4 KB memset per conversion showed in hot loops */
  c.depth = 0;
  c.results_last = false;
  c.adapt = false;
  return convert(&c, value, check, true, out);
}

bool pts_value_try_convert_as_is(pts_value value, const pts_check *check, pts_value *out) {
  converting c; /* as in pts_value_try_convert */
  c.depth = 0;
  c.results_last = false;
  c.adapt = true;
  return convert(&c, value, check, false, out);
}

pts_value pts_value_convert(pts_value value, const pts_check *check) {
  pts_value result;
  if (!pts_value_try_convert(value, check, &result)) {
    pts_throw_type_mismatch(value, check->name);
  }
  return result;
}

pts_value pts_value_convert_as_is(pts_value value, const pts_check *check) {
  pts_value result;
  if (pts_value_try_convert_as_is(value, check, &result)) {
    return result;
  }
  if (pts_value_try_convert(value, check, &result)) { /* a copy, which writes through either wouldn't show in the other */
    pts_throw_new(&pts_type_builtin_TypeError, "Expected %s, got %s, which converts to it only as a copy that wouldn't show later changes", check->name,
                  pts_value_description(value));
  }
  pts_throw_type_mismatch(value, check->name);
}
