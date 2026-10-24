/* Converting dynamic values to the program's types (convert.c): the same value if it fits, a copy of dynamic data, TypeError otherwise. */
#include "pts.h"

#include "check.h"

static const pts_check number_check = {.kind = PTS_CHECK_NUMBER, .slot = PTS_FIELD_NUMBER, .name = "number"};
static const pts_check any_check = {.kind = PTS_CHECK_ANY, .slot = PTS_FIELD_VALUE, .name = "any"};
static const pts_check numbers_check = {
    .kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "number[]", .element = &number_check, .type = &pts_array_types[PTS_FIELD_NUMBER]};
static const pts_check anys_check = {
    .kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "any[]", .element = &any_check, .type = &pts_array_types[PTS_FIELD_VALUE]};
/* any[][]: an array of objects, which the program describes (D-072). */
static const pts_type nested_type = {
    .kind = PTS_KIND_ARRAY,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_array, buffer)),
    .name = "Array",
    .size = sizeof(pts_array),
    .element = PTS_FIELD_OBJECT,
    .check = &anys_check,
};
static const pts_check nested_check = {.kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "any[][]", .element = &anys_check, .type = &nested_type};

static pts_array *values_of(size_t count, const pts_value *values) { return pts_array_of(&pts_array_types[PTS_FIELD_VALUE], values, count); }

static void an_array_of_the_right_kind_is_the_same_array(void) {
  double elements[] = {1, 2};
  pts_array *numbers = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2);
  CHECK(pts_value_convert(pts_value_from_object(numbers), &numbers_check) == pts_value_from_object(numbers));
}

static void an_array_of_any_is_copied_into_numbers(void) {
  pts_value elements[] = {pts_value_from_number(1), pts_value_from_number(2.5)};
  pts_array *loose = values_of(2, elements);
  pts_value converted = pts_value_convert(pts_value_from_object(loose), &numbers_check);
  pts_array *copy = (pts_array *)pts_value_as_object(converted);
  CHECK(copy != loose && copy->type->element == PTS_FIELD_NUMBER);
  CHECK(pts_array_element(copy, 1) == pts_value_from_number(2.5));
}

static void an_element_that_doesnt_fit_throws(void) {
  pts_value elements[] = {pts_value_from_number(1), pts_value_from_string(pts_string_from_cstr("2"))};
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_value_convert(pts_value_from_object(values_of(2, elements)), &numbers_check);
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  }
}

static void a_self_containing_array_converts_without_looping(void) {
  pts_value placeholder = PTS_VALUE_UNDEFINED;
  pts_array *outer = values_of(1, &placeholder);
  pts_value self = pts_value_from_object(outer);
  pts_array_set(outer, 0, &self); /* outer[0] is outer */
  pts_array *copy = (pts_array *)pts_value_as_object(pts_value_convert(self, &nested_check));
  CHECK(copy != outer && copy->type == &nested_type); /* any[][] holds pointers where any[] holds pts_values */
  CHECK(pts_array_element(copy, 0) == self);                        /* an any[] fits as it is */
}

/* [number, any?]: a tuple whose second element is optional (D-100). */
typedef struct {
  pts_object header;
  double first;
  pts_value second;
} optional_pair;
static const pts_field optional_pair_fields[] = {
    {"0", offsetof(optional_pair, first), PTS_FIELD_NUMBER, NULL},
    {"1", offsetof(optional_pair, second), PTS_FIELD_VALUE, NULL},
};
static const pts_type optional_pair_type = {
    .kind = PTS_KIND_TUPLE,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(optional_pair, second)),
    .name = "Array",
    .size = sizeof(optional_pair),
    .field_count = 2,
    .fields = optional_pair_fields,
};
static const pts_check_property optional_pair_elements[] = {
    {"0", &number_check, false, offsetof(optional_pair, first)},
    {"1", &any_check, true, offsetof(optional_pair, second)},
};
static const pts_check optional_pair_check = {
    .kind = PTS_CHECK_TUPLE, .slot = PTS_FIELD_OBJECT, .name = "[number, any?]", .type = &optional_pair_type, .count = 2, .properties = optional_pair_elements};

static bool converts_to_optional_pair(size_t count, const pts_value *values, pts_value *out) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    *out = pts_value_convert(pts_value_from_object(values_of(count, values)), &optional_pair_check);
    pts_try_leave(&handler);
    return true;
  }
  CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  return false;
}

/* An array shorter than a tuple type by its optional elements converts, leaving them out, as `const t: [number, string?] = [1]` (H05 review round 6). */
static void an_array_without_optional_elements_is_copied_into_a_tuple(void) {
  pts_value elements[] = {pts_value_from_number(1), pts_value_from_number(2), pts_value_from_number(3)};
  pts_value out;
  bool converted = converts_to_optional_pair(1, elements, &out);
  CHECK(converted);
  if (!converted) {
    return;
  }
  CHECK(pts_value_as_object(out)->type == &optional_pair_type && pts_tuple_length(pts_value_as_object(out)) == 1);
  CHECK(((optional_pair *)pts_value_as_object(out))->first == 1);
  CHECK(converts_to_optional_pair(2, elements, &out) && pts_tuple_length(pts_value_as_object(out)) == 2);
  CHECK(!converts_to_optional_pair(0, elements, &out)); /* the required element is missing */
  CHECK(!converts_to_optional_pair(3, elements, &out)); /* one too many */
}

/* A tuple is an array in JS, so one converts to an array type as an array of other elements does: a copy with each element converted (C70, D-190). */
static void a_tuple_is_copied_into_an_array(void) {
  optional_pair *pair = pts_object_new(&optional_pair_type);
  pair->first = 1;
  pair->second = pts_value_from_number(2.5);
  pts_array *copy = (pts_array *)pts_value_as_object(pts_value_convert(pts_value_from_object(pair), &numbers_check));
  CHECK(copy->type->element == PTS_FIELD_NUMBER && pts_array_length_of(copy) == 2);
  CHECK(pts_array_element(copy, 0) == pts_value_from_number(1) && pts_array_element(copy, 1) == pts_value_from_number(2.5));
  pair->second = PTS_VALUE_ABSENT; /* the optional element left out: no element for it */
  copy = (pts_array *)pts_value_as_object(pts_value_convert(pts_value_from_object(pair), &numbers_check));
  CHECK(pts_array_length_of(copy) == 1);
  pair->second = pts_value_from_string(pts_string_from_cstr("2"));
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_value_convert(pts_value_from_object(pair), &numbers_check);
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  }
}

/*
 * Opt<number> = { tag: "some"; value: number[] } | { tag: "none" }: a union of object types
 * whose members' copy shapes are some_layout and none_layout (D-071), and an erased copy's
 * object of it, whose value is an any[] (C187).
 */
typedef struct some_layout {
  pts_object header;
  pts_string *tag;
  pts_array *value;
} some_layout;
typedef struct none_layout {
  pts_object header;
  pts_string *tag;
} none_layout;
static const pts_check string_check = {.kind = PTS_CHECK_STRING, .slot = PTS_FIELD_STRING, .name = "string"};
static const pts_field some_fields[] = {
    {"tag", offsetof(some_layout, tag), PTS_FIELD_STRING, &string_check},
    {"value", offsetof(some_layout, value), PTS_FIELD_OBJECT, &numbers_check},
};
static const pts_type some_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(some_layout, tag) | PTS_GC_WORD(some_layout, value)),
    .name = "Object",
    .size = sizeof(some_layout),
    .field_count = 2,
    .fields = some_fields,
};
static const pts_field none_fields[] = {{"tag", offsetof(none_layout, tag), PTS_FIELD_STRING, &string_check}};
static const pts_type none_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(none_layout, tag)),
    .name = "Object",
    .size = sizeof(none_layout),
    .field_count = 1,
    .fields = none_fields,
};
static const pts_check_property some_properties[] = {
    {"tag", &string_check, false, offsetof(some_layout, tag)},
    {"value", &numbers_check, false, offsetof(some_layout, value)},
};
static const pts_check some_check = {
    .kind = PTS_CHECK_OBJECT, .slot = PTS_FIELD_OBJECT, .name = "{tag: string; value: number[]}", .count = 2, .properties = some_properties, .type = &some_type};
static const pts_check_property none_properties[] = {{"tag", &string_check, false, offsetof(none_layout, tag)}};
static const pts_check none_check = {
    .kind = PTS_CHECK_OBJECT, .slot = PTS_FIELD_OBJECT, .name = "{tag: string}", .count = 1, .properties = none_properties, .type = &none_type};
static const pts_check *const opt_members[] = {&none_check, &some_check}; /* the member the object fits as it is first */
static const pts_check opt_check = {.kind = PTS_CHECK_UNION, .slot = PTS_FIELD_VALUE, .name = "Opt<number>", .count = 2, .members = opt_members};

/* The erased copy's layout of the "some" member: its value declared any[]. */
static const pts_field erased_some_fields[] = {
    {"tag", offsetof(some_layout, tag), PTS_FIELD_STRING, &string_check},
    {"value", offsetof(some_layout, value), PTS_FIELD_OBJECT, &anys_check},
};
static const pts_type erased_some_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(some_layout, tag) | PTS_GC_WORD(some_layout, value)),
    .name = "Object",
    .size = sizeof(some_layout),
    .field_count = 2,
    .fields = erased_some_fields,
};

static some_layout *some_of(const pts_type *type, pts_array *value) {
  some_layout *some = pts_object_new(type);
  some->tag = pts_string_from_cstr("some");
  some->value = value;
  return some;
}

/* An erased copy's object fits `{ tag: "none" }` as it is, but has a property only the other member declares: it is copied into that one (C187, D-192). */
static void an_object_is_copied_into_the_member_that_declares_its_properties(void) {
  pts_value seven = pts_value_from_number(7);
  some_layout *erased = some_of(&erased_some_type, values_of(1, &seven));
  pts_value converted = pts_value_convert(pts_value_from_object(erased), &opt_check);
  some_layout *copy = (some_layout *)pts_value_as_object(converted);
  CHECK(copy != erased && copy->header.type == &some_type);
  CHECK(copy->value->type->element == PTS_FIELD_NUMBER && pts_array_element(copy->value, 0) == seven);
}

/* Objects that a member takes as it is and that declare no property it leaves out stay the same objects. */
static void objects_of_a_members_types_stay_the_same(void) {
  double seven = 7;
  some_layout *some = some_of(&some_type, pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], &seven, 1));
  CHECK(pts_value_convert(pts_value_from_object(some), &opt_check) == pts_value_from_object(some));
  none_layout *none = pts_object_new(&none_type);
  none->tag = pts_string_from_cstr("none");
  CHECK(pts_value_convert(pts_value_from_object(none), &opt_check) == pts_value_from_object(none));
}

/* One that converts to no member that declares its properties is taken as it is by one that leaves them out (a lie, which typed reads then find). */
static void an_object_that_converts_to_no_declaring_member_is_taken_as_it_is(void) {
  pts_value text = pts_value_from_string(pts_string_from_cstr("x"));
  some_layout *erased = some_of(&erased_some_type, values_of(1, &text));
  CHECK(pts_value_convert(pts_value_from_object(erased), &opt_check) == pts_value_from_object(erased));
}

static const pts_type any_map_type = {
    .kind = PTS_KIND_MAP,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Map",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_STRING,
    .element = PTS_FIELD_VALUE,
};
static const pts_type number_map_type = {
    .kind = PTS_KIND_MAP,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Map",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_STRING,
    .element = PTS_FIELD_NUMBER,
    .key_check = &string_check,
    .check = &number_check,
};
static const pts_check number_map_check = {
    .kind = PTS_CHECK_MAP, .slot = PTS_FIELD_OBJECT, .name = "Map<string, number>", .type = &number_map_type, .element = &number_check};

static pts_map *map_of(const pts_type *type, const char *key, pts_value value) {
  pts_map *map = pts_map_new(type);
  pts_string *k = pts_string_from_cstr(key);
  uint64_t slot = 0;
  pts_store_value_in_slot(type->element, &slot, value);
  pts_map_set(map, &k, &slot);
  return map;
}

/* A map of other value types is copied with each value converted, as an array is (D-192); one of the target's types stays the same map. */
static void a_map_of_other_value_types_is_copied(void) {
  pts_map *loose = map_of(&any_map_type, "k", pts_value_from_number(7.5));
  pts_map *copy = (pts_map *)pts_value_as_object(pts_value_convert(pts_value_from_object(loose), &number_map_check));
  pts_string *key = pts_string_from_cstr("k");
  double value = 0;
  CHECK(copy != loose && copy->type == &number_map_type && pts_map_get(copy, &key, &value) && value == 7.5);
  CHECK(pts_value_convert(pts_value_from_object(copy), &number_map_check) == pts_value_from_object(copy));
  pts_map *wrong = map_of(&any_map_type, "k", pts_value_from_string(pts_string_from_cstr("x")));
  pts_value out;
  CHECK(!pts_value_try_convert(pts_value_from_object(wrong), &number_map_check, &out));
}

/* pts_value_convert_as_is (D-219): the value itself where it is one of the type already, else TypeError, never a copy. */
static void as_is_takes_an_array_of_the_type_itself(void) {
  pts_value elements[] = {pts_value_from_number(1)};
  pts_array *loose = values_of(1, elements);
  CHECK(pts_value_convert_as_is(pts_value_from_object(loose), &anys_check) == pts_value_from_object(loose));
}

static void as_is_throws_where_only_a_copy_converts(void) {
  double elements[] = {1, 2};
  pts_array *numbers = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2);
  const pts_check *targets[] = {&anys_check, &number_check};
  for (size_t i = 0; i < sizeof targets / sizeof targets[0]; i++) { /* a number[] as an any[] (a copy); an array as a number (no conversion) */
    pts_handler handler;
    pts_try_enter(&handler);
    if (_setjmp(handler.jump) == 0) {
      pts_value_convert_as_is(pts_value_from_object(numbers), targets[i]);
      CHECK(false);
    } else {
      CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
    }
  }
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  an_array_of_the_right_kind_is_the_same_array();
  an_array_of_any_is_copied_into_numbers();
  an_element_that_doesnt_fit_throws();
  a_self_containing_array_converts_without_looping();
  an_array_without_optional_elements_is_copied_into_a_tuple();
  a_tuple_is_copied_into_an_array();
  an_object_is_copied_into_the_member_that_declares_its_properties();
  objects_of_a_members_types_stay_the_same();
  an_object_that_converts_to_no_declaring_member_is_taken_as_it_is();
  a_map_of_other_value_types_is_copied();
  as_is_takes_an_array_of_the_type_itself();
  as_is_throws_where_only_a_copy_converts();
  return pts_runtime_exit(CHECK_DONE());
}
