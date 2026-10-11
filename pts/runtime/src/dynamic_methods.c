/*
 * Builtin methods called through `any` (D-076): the methods of strings, numbers, booleans,
 * arrays and functions, found by name on the receiver's kind. Each takes its arguments as
 * dynamic values with JS's conversions (an index is ToNumber of its argument, NaN reading
 * as 0 as the runtime's methods do; a string is ToString of its argument) and returns a
 * dynamic value. Arrays of any element type are read through their boxed elements and
 * written with values converted to their declared element type; the arrays they make
 * that JS fills with new values are `any[]`.
 */
#include "dynamic_internal.h"

#include <stdio.h>

typedef pts_value (*builtin_method)(pts_value self, uint32_t argc, const pts_value *argv);

typedef struct named_method {
  const char *name;
  builtin_method method;
} named_method;

/* ---------------------------------------------------------------- arguments */

static pts_value argument(uint32_t argc, const pts_value *argv, uint32_t i) { return i < argc ? argv[i] : PTS_VALUE_UNDEFINED; }

/* An index or count: ToNumber of the argument, or `missing` if it is undefined (NaN for 0, Infinity for the length). */
static double number_argument(uint32_t argc, const pts_value *argv, uint32_t i, double missing) {
  pts_value value = argument(argc, argv, i);
  return value == PTS_VALUE_UNDEFINED ? missing : pts_value_to_number(value);
}

/* A string: ToString of the argument, or `missing` if it is undefined and missing isn't NULL. */
static pts_string *string_argument(uint32_t argc, const pts_value *argv, uint32_t i, pts_string *missing) {
  pts_value value = argument(argc, argv, i);
  return value == PTS_VALUE_UNDEFINED && missing != NULL ? missing : pts_value_to_string(value);
}

static bool is_function(pts_value value) { return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_FUNCTION; }

/* A callback argument: TypeError, as V8 words it, if it isn't a function (`number 5 is not a function`). */
static pts_closure *function_argument(uint32_t argc, const pts_value *argv, uint32_t i) {
  pts_value value = argument(argc, argv, i);
  if (is_function(value)) {
    return (pts_closure *)pts_value_as_object(value);
  }
  if (value == PTS_VALUE_UNDEFINED) {
    pts_throw_new(&pts_type_builtin_TypeError, "undefined is not a function");
  }
  if (value == PTS_VALUE_NULL || (pts_value_is_object(value) && pts_value_as_object(value)->type->kind != PTS_KIND_STRING)) {
    pts_throw_new(&pts_type_builtin_TypeError, "object%s is not a function", value == PTS_VALUE_NULL ? " null" : "");
  }
  pts_string *shown = pts_value_to_string(value);
  const char *quote = pts_value_is_object(value) ? "\"" : ""; /* a string */
  pts_throw_new(&pts_type_builtin_TypeError, "%s %s%.*s%s is not a function", pts_value_typeof(value)->bytes, quote, (int)shown->byte_len,
                shown->bytes, quote);
}

static double integer_or_infinity(double x) { return x != x ? 0 : __builtin_trunc(x); }

/* A relative index (JS's slice and at): from the end if negative, clamped to [0, length]. */
static double relative(double index, double length) {
  index = integer_or_infinity(index);
  if (index < 0) {
    return index + length < 0 ? 0 : index + length;
  }
  return index > length ? length : index;
}

/* ---------------------------------------------------------------- strings */

static pts_string *string_of(pts_value self) { return (pts_string *)pts_value_as_object(self); }

static pts_value string_char_at(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value c = pts_string_element(string_of(self), integer_or_infinity(number_argument(argc, argv, 0, 0)));
  return c == PTS_VALUE_UNDEFINED ? pts_value_from_string(pts_string_from_cstr("")) : c;
}

static pts_value string_at(pts_value self, uint32_t argc, const pts_value *argv) {
  double index = integer_or_infinity(number_argument(argc, argv, 0, 0));
  return pts_string_element(string_of(self), index < 0 ? index + string_of(self)->utf16_len : index);
}

static pts_value string_char_code_at(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(pts_string_char_code_at(string_of(self), number_argument(argc, argv, 0, 0)));
}

static pts_value string_slice(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(
      pts_string_slice(string_of(self), number_argument(argc, argv, 0, __builtin_nan("")), number_argument(argc, argv, 1, __builtin_inf())));
}

static pts_value string_substring(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_string_substring(string_of(self), number_argument(argc, argv, 0, __builtin_nan("")),
                                                    number_argument(argc, argv, 1, __builtin_inf())));
}

static pts_value string_index_of(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(
      pts_string_index_of(string_of(self), string_argument(argc, argv, 0, NULL), number_argument(argc, argv, 1, __builtin_nan(""))));
}

static pts_value string_includes(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_bool(
      pts_string_includes(string_of(self), string_argument(argc, argv, 0, NULL), number_argument(argc, argv, 1, __builtin_nan(""))));
}

static pts_value string_starts_with(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_bool(
      pts_string_starts_with(string_of(self), string_argument(argc, argv, 0, NULL), number_argument(argc, argv, 1, __builtin_nan(""))));
}

static pts_value string_ends_with(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_bool(
      pts_string_ends_with(string_of(self), string_argument(argc, argv, 0, NULL), number_argument(argc, argv, 1, __builtin_inf())));
}

#define STRING_TO_STRING(name, function)                                           \
  static pts_value name(pts_value self, uint32_t argc, const pts_value *argv) {    \
    (void)argc, (void)argv;                                                        \
    return pts_value_from_string(function(string_of(self)));                       \
  }
STRING_TO_STRING(string_to_upper_case, pts_string_to_upper_case)
STRING_TO_STRING(string_to_lower_case, pts_string_to_lower_case)
STRING_TO_STRING(string_trim, pts_string_trim)
STRING_TO_STRING(string_trim_start, pts_string_trim_start)
STRING_TO_STRING(string_trim_end, pts_string_trim_end)
#undef STRING_TO_STRING

static pts_value string_pad_start(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_string_pad_start(string_of(self), number_argument(argc, argv, 0, 0),
                                                    string_argument(argc, argv, 1, pts_string_from_cstr(" "))));
}

static pts_value string_pad_end(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(
      pts_string_pad_end(string_of(self), number_argument(argc, argv, 0, 0), string_argument(argc, argv, 1, pts_string_from_cstr(" "))));
}

static pts_value string_repeat(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_string_repeat(string_of(self), number_argument(argc, argv, 0, 0)));
}

static pts_value string_split(pts_value self, uint32_t argc, const pts_value *argv) {
  if (argument(argc, argv, 0) == PTS_VALUE_UNDEFINED) { /* no separator: the whole string */
    pts_string *whole = string_of(self);
    return pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_STRING], &whole, number_argument(argc, argv, 1, 1) == 0 ? 0 : 1));
  }
  return pts_value_from_object(
      pts_string_split(string_of(self), string_argument(argc, argv, 0, NULL), number_argument(argc, argv, 1, __builtin_inf())));
}

static pts_value string_concat(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_string *result = string_of(self);
  for (uint32_t i = 0; i < argc; i++) {
    result = pts_string_concat(result, pts_value_to_string(argv[i]));
  }
  return pts_value_from_string(result);
}

static pts_value string_code_point_at(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_string_code_point_at(string_of(self), number_argument(argc, argv, 0, 0));
}

static pts_value string_last_index_of(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(
      pts_string_last_index_of(string_of(self), string_argument(argc, argv, 0, NULL), number_argument(argc, argv, 1, __builtin_nan(""))));
}

static pts_value string_substr(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_string_substr(string_of(self), number_argument(argc, argv, 0, __builtin_nan("")),
                                                 number_argument(argc, argv, 1, __builtin_inf())));
}

/* Whether a locale argument is undefined or an English locale, whose collation and case mappings are the runtime's (D-082). */
static bool is_default_locale(pts_value locale) {
  if (locale == PTS_VALUE_UNDEFINED) {
    return true;
  }
  if (!pts_value_is_object(locale) || pts_value_as_object(locale)->type->kind != PTS_KIND_STRING) {
    return false;
  }
  const pts_string *tag = (const pts_string *)pts_value_as_object(locale);
  return tag->byte_len >= 2 && (tag->bytes[0] | 0x20) == 'e' && (tag->bytes[1] | 0x20) == 'n' && (tag->byte_len == 2 || tag->bytes[2] == '-');
}

static void check_default_locale(uint32_t argc, const pts_value *argv, uint32_t i, const char *method) {
  if (!is_default_locale(argument(argc, argv, i))) {
    pts_throw_new(&pts_type_builtin_TypeError, "%s with a locale other than English through `any` is not supported yet", method);
  }
}

static pts_value string_locale_compare(pts_value self, uint32_t argc, const pts_value *argv) {
  check_default_locale(argc, argv, 1, "localeCompare");
  if (argument(argc, argv, 2) != PTS_VALUE_UNDEFINED) {
    pts_throw_new(&pts_type_builtin_TypeError, "localeCompare with options through `any` is not supported yet");
  }
  return pts_value_from_number(pts_string_locale_compare(string_of(self), string_argument(argc, argv, 0, NULL)));
}

static pts_value string_to_locale_upper_case(pts_value self, uint32_t argc, const pts_value *argv) {
  check_default_locale(argc, argv, 0, "toLocaleUpperCase");
  return pts_value_from_string(pts_string_to_upper_case(string_of(self)));
}

static pts_value string_to_locale_lower_case(pts_value self, uint32_t argc, const pts_value *argv) {
  check_default_locale(argc, argv, 0, "toLocaleLowerCase");
  return pts_value_from_string(pts_string_to_lower_case(string_of(self)));
}

/* replace and replaceAll with a callback: each match replaced by ToString of callback(match, position, string). */
static pts_value replace_with_callback(pts_value self, pts_string *search, pts_closure *callback, bool all) {
  pts_string *string = string_of(self);
  double position = pts_string_index_of(string, search, 0);
  if (position < 0) {
    return self;
  }
  pts_string *result = pts_string_slice(string, 0, 0);
  double end = 0, length = search->utf16_len;
  while (position >= 0) {
    pts_value args[] = {pts_value_from_string(search), pts_value_from_number(position), self};
    pts_string *replacement = pts_value_to_string(pts_dynamic_call_closure(callback, 3, args));
    result = pts_string_concat(pts_string_concat(result, pts_string_slice(string, end, position)), replacement);
    end = position + length;
    double next = position + (length == 0 ? 1 : length);
    position = !all || next > string->utf16_len ? -1 : pts_string_index_of(string, search, next);
  }
  return pts_value_from_string(pts_string_concat(result, pts_string_slice(string, end, __builtin_inf())));
}

static pts_value string_replacing(pts_value self, uint32_t argc, const pts_value *argv, bool all) {
  pts_string *search = string_argument(argc, argv, 0, NULL);
  pts_value replacement = argument(argc, argv, 1);
  if (is_function(replacement)) {
    return replace_with_callback(self, search, (pts_closure *)pts_value_as_object(replacement), all);
  }
  pts_string *text = pts_value_to_string(replacement);
  return pts_value_from_string(all ? pts_string_replace_all(string_of(self), search, text) : pts_string_replace(string_of(self), search, text));
}

static pts_value string_replace(pts_value self, uint32_t argc, const pts_value *argv) { return string_replacing(self, argc, argv, false); }

static pts_value string_replace_all(pts_value self, uint32_t argc, const pts_value *argv) { return string_replacing(self, argc, argv, true); }

static pts_value string_is_well_formed(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_value_from_bool(pts_string_is_well_formed(string_of(self)));
}

static pts_value string_to_well_formed(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_value_from_string(pts_string_to_well_formed(string_of(self)));
}

/* toString and valueOf of a primitive: itself, as a string for toString. */
static pts_value primitive_value_of(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return self;
}

static pts_value primitive_to_string(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value radix = argument(argc, argv, 0);
  if (radix != PTS_VALUE_UNDEFINED && pts_value_to_number(radix) != 10) {
    pts_throw_new(&pts_type_builtin_TypeError, "toString with a radix through `any` is not supported yet");
  }
  return pts_value_from_string(pts_value_to_string(self));
}

static const named_method string_methods[] = {
    {"at", string_at},
    {"charAt", string_char_at},
    {"charCodeAt", string_char_code_at},
    {"codePointAt", string_code_point_at},
    {"concat", string_concat},
    {"endsWith", string_ends_with},
    {"includes", string_includes},
    {"indexOf", string_index_of},
    {"isWellFormed", string_is_well_formed},
    {"lastIndexOf", string_last_index_of},
    {"localeCompare", string_locale_compare},
    {"padEnd", string_pad_end},
    {"padStart", string_pad_start},
    {"repeat", string_repeat},
    {"replace", string_replace},
    {"replaceAll", string_replace_all},
    {"slice", string_slice},
    {"split", string_split},
    {"startsWith", string_starts_with},
    {"substr", string_substr},
    {"substring", string_substring},
    {"toLocaleLowerCase", string_to_locale_lower_case},
    {"toLocaleUpperCase", string_to_locale_upper_case},
    {"toLowerCase", string_to_lower_case},
    {"toString", primitive_value_of},
    {"toUpperCase", string_to_upper_case},
    {"toWellFormed", string_to_well_formed},
    {"trim", string_trim},
    {"trimEnd", string_trim_end},
    {"trimLeft", string_trim_start},
    {"trimRight", string_trim_end},
    {"trimStart", string_trim_start},
    {"valueOf", primitive_value_of},
    {NULL, NULL},
};

static const named_method primitive_methods[] = {
    {"toString", primitive_to_string},
    {"valueOf", primitive_value_of},
    {NULL, NULL},
};

/* ---------------------------------------------------------------- arrays */

static pts_array *array_of(pts_value self) { return (pts_array *)pts_value_as_object(self); }

/* A new any[] that the elements of `any` arrays and values are pushed to. */
static pts_array *any_array(void) { return pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0); }

static void push_any(pts_array *array, pts_value value) { pts_array_push(array, &value, 1); }

/*
 * Converts values to array's element type (its declared type, D-072: TypeError if one
 * doesn't fit) into a new buffer of count elements of its kind, as the array methods
 * that take elements by address want them.
 */
static void *elements_for(pts_array *array, uint32_t count, const pts_value *values) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  unsigned char *buffer = GC_MALLOC(count * size + 1);
  if (buffer == NULL) {
    pts_panic("RangeError", "Out of memory converting %u elements", count);
  }
  for (uint32_t i = 0; i < count; i++) {
    pts_store_value_in_slot(kind, buffer + i * size, pts_storable(kind, array->type->check, values[i], "element", array->type));
  }
  return buffer;
}

static pts_value array_push(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  return pts_value_from_number(pts_array_push(array, elements_for(array, argc, argv), argc));
}

static pts_value array_unshift(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  return pts_value_from_number(pts_array_unshift(array, elements_for(array, argc, argv), argc));
}

static pts_value array_pop(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_array_pop_value(array_of(self));
}

static pts_value array_shift(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_array_shift_value(array_of(self));
}

static pts_value array_slice(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_object(
      pts_array_slice(array_of(self), number_argument(argc, argv, 0, __builtin_nan("")), number_argument(argc, argv, 1, __builtin_inf())));
}

static pts_value array_splice(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  double start = number_argument(argc, argv, 0, __builtin_nan(""));
  double delete_count = argc < 2 ? __builtin_inf() : number_argument(argc, argv, 1, 0);
  uint32_t count = argc > 2 ? argc - 2 : 0;
  return pts_value_from_object(pts_array_splice(array, start, delete_count, elements_for(array, count, argv + 2), count));
}

/* JS's IsConcatSpreadable for ParallelScript's values: arrays and tuples. */
static bool is_array_like(pts_value value) {
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_kind kind = pts_value_as_object(value)->type->kind;
  return kind == PTS_KIND_ARRAY || kind == PTS_KIND_TUPLE;
}

static pts_value array_concat(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *result = any_array();
  for (uint32_t i = 0; i <= argc; i++) {
    pts_value part = i == 0 ? self : argv[i - 1];
    if (!is_array_like(part)) {
      push_any(result, part);
      continue;
    }
    for (size_t j = 0, length = pts_spread_length(part); j < length; j++) {
      push_any(result, pts_spread_element(part, j));
    }
  }
  return pts_value_from_object(result);
}

static pts_value array_join(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_array_join(array_of(self), string_argument(argc, argv, 0, pts_string_from_cstr(","))));
}

static pts_value array_to_string(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_value_from_string(pts_array_join(array_of(self), pts_string_from_cstr(",")));
}

/* SameValueZero, which includes compares with: ===, but NaN is NaN. */
static bool same_value_zero(pts_value a, pts_value b) {
  if (pts_value_strict_equals(a, b)) {
    return true;
  }
  return pts_value_is_number(a) && pts_value_is_number(b) && pts_value_as_number(a) != pts_value_as_number(a) &&
         pts_value_as_number(b) != pts_value_as_number(b);
}

/* The first index from `from` (relative) whose element equals value, by === or SameValueZero; -1 if none. */
static double search_forward(pts_array *array, pts_value value, double from, bool zero) {
  double length = pts_array_length(array);
  for (double i = relative(from, length); i < pts_array_length(array); i++) {
    pts_value element = pts_array_element(array, i);
    if (zero ? same_value_zero(element, value) : pts_value_strict_equals(element, value)) {
      return i;
    }
  }
  return -1;
}

static pts_value array_index_of(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(search_forward(array_of(self), argument(argc, argv, 0), number_argument(argc, argv, 1, 0), false));
}

static pts_value array_includes(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_bool(search_forward(array_of(self), argument(argc, argv, 0), number_argument(argc, argv, 1, 0), true) >= 0);
}

static pts_value array_last_index_of(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  double length = pts_array_length(array);
  double from = argc > 1 ? integer_or_infinity(pts_value_to_number(argv[1])) : length - 1;
  from = from < 0 ? from + length : from > length - 1 ? length - 1 : from;
  for (double i = from; i >= 0; i--) {
    if (pts_value_strict_equals(pts_array_element(array, i), argument(argc, argv, 0))) {
      return pts_value_from_number(i);
    }
  }
  return pts_value_from_number(-1);
}

static pts_value array_at(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  double index = integer_or_infinity(number_argument(argc, argv, 0, 0));
  return pts_array_element(array, index < 0 ? index + pts_array_length(array) : index);
}

static pts_value array_reverse(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  pts_array_reverse(array_of(self));
  return self;
}

static pts_value array_fill(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  pts_value value = argument(argc, argv, 0);
  pts_array_fill(array, elements_for(array, 1, &value), number_argument(argc, argv, 1, __builtin_nan("")),
                 number_argument(argc, argv, 2, __builtin_inf()));
  return self;
}

static void flatten_into(pts_array *result, pts_value part, double depth) {
  for (size_t i = 0, length = pts_spread_length(part); i < length; i++) {
    pts_value element = pts_spread_element(part, i);
    if (depth >= 1 && is_array_like(element)) {
      flatten_into(result, element, depth - 1);
    } else {
      push_any(result, element);
    }
  }
}

static pts_value array_flat(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *result = any_array();
  flatten_into(result, self, integer_or_infinity(number_argument(argc, argv, 0, 1)));
  return pts_value_from_object(result);
}

/* The element kind of the array a comparator sorts, for the comparator's call: sorts nest only through comparators. */
static _Thread_local pts_field_kind sorting_kind;

static double compare_elements(pts_closure *compare, const void *a, const void *b) {
  pts_value pair[] = {pts_value_of_slot(sorting_kind, a), pts_value_of_slot(sorting_kind, b)};
  pts_field_kind kind = sorting_kind;
  double order = pts_value_to_number(pts_dynamic_call_closure(compare, 2, pair));
  sorting_kind = kind;
  return order != order ? 0 : order;
}

static pts_value array_sort(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  if (argument(argc, argv, 0) == PTS_VALUE_UNDEFINED) {
    pts_array_sort(array);
    return self;
  }
  pts_value compare = argv[0];
  if (!is_function(compare)) {
    bool object = pts_value_is_object(compare) && pts_value_as_object(compare)->type->kind != PTS_KIND_STRING;
    pts_string *shown = object ? NULL : pts_value_to_string(compare);
    if (object) {
      pts_throw_new(&pts_type_builtin_TypeError, "The comparison function must be either a function or undefined: #<%s>", pts_value_description(compare));
    }
    pts_throw_new(&pts_type_builtin_TypeError, "The comparison function must be either a function or undefined: %.*s", (int)shown->byte_len, shown->bytes);
  }
  pts_field_kind outer = sorting_kind;
  sorting_kind = array->type->element;
  pts_array_sort_with(array, (pts_closure *)pts_value_as_object(compare), compare_elements);
  sorting_kind = outer;
  return self;
}

/* callback(element, index, array) for the element at index. */
static pts_value call_back(pts_closure *callback, pts_value self, double index) {
  pts_value args[] = {pts_array_element(array_of(self), index), pts_value_from_number(index), self};
  return pts_dynamic_call_closure(callback, 3, args);
}

static pts_value array_for_each(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  for (double i = 0; i < pts_array_length(array_of(self)); i++) {
    call_back(callback, self, i);
  }
  return PTS_VALUE_UNDEFINED;
}

static pts_value array_map(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *result = any_array();
  for (double i = 0, length = pts_array_length(array_of(self)); i < length && i < pts_array_length(array_of(self)); i++) {
    push_any(result, call_back(callback, self, i));
  }
  return pts_value_from_object(result);
}

static pts_value array_filter(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *array = array_of(self);
  pts_array *result = pts_array_of(array->type, NULL, 0); /* the same elements, so the same type */
  for (double i = 0; i < pts_array_length(array); i++) {
    pts_value element = pts_array_element(array, i);
    if (pts_value_truthy(call_back(callback, self, i))) {
      uint64_t slot = 0;
      pts_store_value_in_slot(array->type->element, &slot, element);
      pts_array_push(result, &slot, 1);
    }
  }
  return pts_value_from_object(result);
}

static pts_value array_flat_map(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *result = any_array();
  for (double i = 0; i < pts_array_length(array_of(self)); i++) {
    pts_value mapped = call_back(callback, self, i);
    if (is_array_like(mapped)) {
      flatten_into(result, mapped, 0);
    } else {
      push_any(result, mapped);
    }
  }
  return pts_value_from_object(result);
}

/* The index of the first (or last, if backwards) element callback is truthy for, or -1. */
static double find_index(pts_value self, uint32_t argc, const pts_value *argv, bool backwards) {
  pts_closure *callback = function_argument(argc, argv, 0);
  double length = pts_array_length(array_of(self));
  for (double n = 0; n < length; n++) {
    double i = backwards ? length - 1 - n : n;
    if (pts_value_truthy(call_back(callback, self, i))) {
      return i;
    }
  }
  return -1;
}

static pts_value array_find(pts_value self, uint32_t argc, const pts_value *argv) {
  double i = find_index(self, argc, argv, false);
  return i < 0 ? PTS_VALUE_UNDEFINED : pts_array_element(array_of(self), i);
}

static pts_value array_find_last(pts_value self, uint32_t argc, const pts_value *argv) {
  double i = find_index(self, argc, argv, true);
  return i < 0 ? PTS_VALUE_UNDEFINED : pts_array_element(array_of(self), i);
}

static pts_value array_find_index(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(find_index(self, argc, argv, false));
}

static pts_value array_find_last_index(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(find_index(self, argc, argv, true));
}

static pts_value array_some(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_bool(find_index(self, argc, argv, false) >= 0);
}

static pts_value array_every(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  for (double i = 0; i < pts_array_length(array_of(self)); i++) {
    if (!pts_value_truthy(call_back(callback, self, i))) {
      return PTS_VALUE_FALSE;
    }
  }
  return PTS_VALUE_TRUE;
}

/* reduce and reduceRight: callback(accumulator, element, index, array) over the elements in order or backwards. */
static pts_value reduce(pts_value self, uint32_t argc, const pts_value *argv, bool backwards) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *array = array_of(self);
  double length = pts_array_length(array), n = 0;
  pts_value accumulator;
  if (argc >= 2) {
    accumulator = argv[1];
  } else if (length == 0) {
    pts_throw_new(&pts_type_builtin_TypeError, "Reduce of empty array with no initial value");
  } else {
    accumulator = pts_array_element(array, backwards ? length - 1 : 0);
    n = 1;
  }
  for (; n < length; n++) {
    double i = backwards ? length - 1 - n : n;
    pts_value args[] = {accumulator, pts_array_element(array, i), pts_value_from_number(i), self};
    accumulator = pts_dynamic_call_closure(callback, 4, args);
  }
  return accumulator;
}

static pts_value array_reduce(pts_value self, uint32_t argc, const pts_value *argv) { return reduce(self, argc, argv, false); }

static pts_value array_reduce_right(pts_value self, uint32_t argc, const pts_value *argv) { return reduce(self, argc, argv, true); }

static const named_method array_methods[] = {
    {"at", array_at},
    {"concat", array_concat},
    {"every", array_every},
    {"fill", array_fill},
    {"filter", array_filter},
    {"find", array_find},
    {"findIndex", array_find_index},
    {"findLast", array_find_last},
    {"findLastIndex", array_find_last_index},
    {"flat", array_flat},
    {"flatMap", array_flat_map},
    {"forEach", array_for_each},
    {"includes", array_includes},
    {"indexOf", array_index_of},
    {"join", array_join},
    {"lastIndexOf", array_last_index_of},
    {"map", array_map},
    {"pop", array_pop},
    {"push", array_push},
    {"reduce", array_reduce},
    {"reduceRight", array_reduce_right},
    {"reverse", array_reverse},
    {"shift", array_shift},
    {"slice", array_slice},
    {"some", array_some},
    {"sort", array_sort},
    {"splice", array_splice},
    {"toString", array_to_string},
    {"unshift", array_unshift},
    {NULL, NULL},
};

/* ---------------------------------------------------------------- functions */

static pts_closure *function_of(pts_value self) { return (pts_closure *)pts_value_as_object(self); }

static pts_value function_call(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_dynamic_call_closure(function_of(self), argc > 0 ? argc - 1 : 0, argv + (argc > 0)); /* `this` is for methods only */
}

static pts_value function_apply(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value list = argument(argc, argv, 1);
  if (pts_value_is_nullish(list)) {
    return pts_dynamic_call_closure(function_of(self), 0, NULL);
  }
  bool spread = true;
  return pts_dynamic_call_spread(self, pts_string_from_cstr("apply"), 1, &list, &spread);
}

/* A function that `bind` made through `any`: it calls `function` with `args` before its own arguments. */
typedef struct bound_function {
  pts_closure closure;
  pts_closure *function; /* first, as console.log expects of bound functions */
  uint32_t count;
  pts_value *args;
} bound_function;

static pts_value bound_function_call(pts_closure *closure, uint32_t argc, const pts_value *argv) {
  const bound_function *bound = (const bound_function *)closure;
  pts_value *all = GC_MALLOC(((size_t)bound->count + argc) * sizeof(pts_value) + 1);
  if (all == NULL) {
    pts_panic("RangeError", "Out of memory calling a bound function");
  }
  memcpy(all, bound->args, bound->count * sizeof(pts_value));
  memcpy(all + bound->count, argv, argc * sizeof(pts_value));
  return pts_dynamic_call_closure(bound->function, bound->count + argc, all);
}

static const pts_type bound_function_type = {
    .kind = PTS_KIND_FUNCTION,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(bound_function, function) | PTS_GC_WORD(bound_function, args)),
    .name = NULL, /* console.log shows "bound " and the name of the function it binds */
    .parent = &pts_bound_function,
    .size = sizeof(bound_function),
    .call = bound_function_call,
};

static pts_value function_bind(pts_value self, uint32_t argc, const pts_value *argv) {
  bound_function *bound = pts_object_new(&bound_function_type);
  bound->function = function_of(self);
  bound->count = argc > 0 ? argc - 1 : 0;
  bound->args = GC_MALLOC(bound->count * sizeof(pts_value) + 1);
  if (bound->args == NULL) {
    pts_panic("RangeError", "Out of memory binding a function");
  }
  memcpy(bound->args, argv + (argc > 0), bound->count * sizeof(pts_value));
  return pts_value_from_object(bound); /* no code and no signature: typed code gets it through an adapter (D-075) */
}

static const named_method function_methods[] = {
    {"apply", function_apply},
    {"bind", function_bind},
    {"call", function_call},
    {NULL, NULL},
};

/* ---------------------------------------------------------------- maps, sets and hash-map objects */

static pts_map *map_of(pts_value self) { return (pts_map *)pts_value_as_object(self); }

/* A key or value as a map's slot of the given kind for a lookup: false if it can't be one, so the map can't have it. */
static bool lookup_slot(pts_field_kind kind, pts_value value, uint64_t *slot) {
  bool fits;
  switch (kind) {
  case PTS_FIELD_VALUE:
    fits = true;
    break;
  case PTS_FIELD_OBJECT:
    fits = pts_value_is_object(value) && !pts_value_is_string(value);
    break;
  case PTS_FIELD_STRING:
    fits = pts_value_is_string(value);
    break;
  case PTS_FIELD_NUMBER:
  case PTS_FIELD_F32:
    fits = pts_value_is_number(value);
    break;
  case PTS_FIELD_BOOL:
    fits = value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE;
    break;
  default:
    fits = pts_value_is_integer_of(value, kind);
  }
  *slot = 0;
  if (fits) {
    pts_store_value_in_slot(kind, slot, value);
  }
  return fits;
}

/* A key or value to store in a map: converted to its declared type (D-072), TypeError if it doesn't fit. */
static uint64_t stored_slot(pts_field_kind kind, const pts_check *check, pts_value value, const char *what, const pts_type *type) {
  uint64_t slot = 0;
  pts_store_value_in_slot(kind, &slot, pts_storable(kind, check, value, what, type));
  return slot;
}

static pts_value map_get(pts_value self, uint32_t argc, const pts_value *argv) {
  uint64_t key;
  pts_map *map = map_of(self);
  return lookup_slot(map->type->key, argument(argc, argv, 0), &key) ? pts_map_get_value(map, &key) : PTS_VALUE_UNDEFINED;
}

static pts_value map_has(pts_value self, uint32_t argc, const pts_value *argv) {
  uint64_t key;
  pts_map *map = map_of(self);
  return pts_value_from_bool(lookup_slot(map->type->key, argument(argc, argv, 0), &key) && pts_map_has(map, &key));
}

static pts_value map_delete(pts_value self, uint32_t argc, const pts_value *argv) {
  uint64_t key;
  pts_map *map = map_of(self);
  return pts_value_from_bool(lookup_slot(map->type->key, argument(argc, argv, 0), &key) && pts_map_delete(map, &key));
}

static pts_value map_clear(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  pts_map_clear(map_of(self));
  return PTS_VALUE_UNDEFINED;
}

static pts_value map_set(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_map *map = map_of(self);
  const pts_type *type = map->type;
  uint64_t key = stored_slot(type->key, type->key_check, argument(argc, argv, 0), "key", type);
  uint64_t value = stored_slot(type->element, type->check, argument(argc, argv, 1), "value", type);
  pts_map_set(map, &key, &value);
  return self;
}

static pts_value set_add(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_map *set = map_of(self);
  uint64_t key = stored_slot(set->type->key, set->type->key_check, argument(argc, argv, 0), "element", set->type);
  pts_map_set(set, &key, NULL);
  return self;
}

/* forEach of a map, callback(value, key, map), or of a set, callback(element, element, set). */
static pts_value map_for_each(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_map *map = map_of(self);
  bool set = map->type->kind == PTS_KIND_SET;
  pts_map_iterator entries = pts_map_iterate(map);
  while (pts_map_next(&entries)) {
    pts_value key = pts_value_of_slot(map->type->key, &entries.key);
    pts_value args[] = {set ? key : pts_value_of_slot(map->type->element, &entries.value), key, self};
    pts_dynamic_call_closure(callback, 3, args);
  }
  return PTS_VALUE_UNDEFINED;
}

static pts_value map_iterator(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  pts_throw_new(&pts_type_builtin_TypeError, "Iterators of maps and sets through `any` are not supported yet");
}

static pts_value record_has_own_property(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_string *key = pts_value_to_string(argument(argc, argv, 0));
  return pts_value_from_bool(pts_map_has(map_of(self), &key));
}

static const named_method map_methods[] = {
    {"clear", map_clear},     {"delete", map_delete}, {"entries", map_iterator}, {"forEach", map_for_each}, {"get", map_get},
    {"has", map_has},         {"keys", map_iterator}, {"set", map_set},          {"values", map_iterator}, {NULL, NULL},
};

static const named_method set_methods[] = {
    {"add", set_add},         {"clear", map_clear},      {"delete", map_delete}, {"entries", map_iterator}, {"forEach", map_for_each},
    {"has", map_has},         {"keys", map_iterator},    {"values", map_iterator}, {NULL, NULL},
};

static const named_method record_methods[] = {
    {"hasOwnProperty", record_has_own_property},
    {NULL, NULL},
};

/* ---------------------------------------------------------------- Object.keys, values, entries */

/* The own enumerable string keys of value, in JS's order, as a string[]. */
static pts_array *own_keys(pts_value value) {
  if (pts_value_is_nullish(value)) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot convert undefined or null to object");
  }
  pts_array *keys = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  if (!pts_value_is_object(value)) {
    return keys; /* a number's or boolean's: none */
  }
  pts_object *object = pts_value_as_object(value);
  switch (object->type->kind) {
  case PTS_KIND_RECORD: {
    pts_map_iterator entries = pts_record_iterate((pts_map *)object);
    while (pts_map_next(&entries)) {
      pts_array_push(keys, &entries.key, 1);
    }
    return keys;
  }
  case PTS_KIND_STRING:
  case PTS_KIND_ARRAY:
  case PTS_KIND_TUPLE: {
    double length = object->type->kind == PTS_KIND_STRING ? ((pts_string *)object)->utf16_len : (double)pts_spread_length(value);
    for (double i = 0; i < length; i++) {
      pts_string *index = pts_number_to_string(i);
      pts_array_push(keys, &index, 1);
    }
    return keys;
  }
  case PTS_KIND_OBJECT:
    break;
  default:
    return keys; /* a map's, set's or function's: none */
  }
  /* An error's own name, message and cause aren't enumerable in JS. */
  uint32_t first = pts_instance_of(object, &pts_type_builtin_Error) ? sizeof(pts_error) : 0;
  for (uint32_t i = 0; i < object->type->field_count; i++) {
    const pts_field *field = &object->type->fields[i];
    if (field->name[0] == '#' || field->offset < first ||
        (!pts_field_is_accessor(field->kind) && field->kind == PTS_FIELD_VALUE &&
         __atomic_load_n((const pts_value *)((const char *)object + field->offset), __ATOMIC_RELAXED) == PTS_VALUE_ABSENT)) {
      continue; /* private, an error's, or an optional property left out */
    }
    pts_string *name = pts_string_from_cstr(field->name);
    pts_array_push(keys, &name, 1);
  }
  return keys;
}

pts_value pts_value_keys(pts_value value) { return pts_value_from_object(own_keys(value)); }

pts_value pts_value_values(pts_value value) {
  pts_array *keys = own_keys(value), *values = any_array();
  for (double i = 0; i < pts_array_length(keys); i++) {
    push_any(values, pts_dynamic_get(value, pts_array_element(keys, i)));
  }
  return pts_value_from_object(values);
}

pts_value pts_value_entries(pts_value value) {
  pts_array *keys = own_keys(value), *entries = any_array();
  for (double i = 0; i < pts_array_length(keys); i++) {
    pts_value key = pts_array_element(keys, i);
    pts_value pair[] = {key, pts_dynamic_get(value, key)};
    push_any(entries, pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_VALUE], pair, 2)));
  }
  return pts_value_from_object(entries);
}

bool pts_value_has_own(pts_value value, pts_value key) {
  pts_array *keys = own_keys(value);
  pts_value name = pts_value_from_string(pts_value_to_string(key));
  for (double i = 0; i < pts_array_length(keys); i++) {
    if (pts_value_strict_equals(pts_array_element(keys, i), name)) {
      return true;
    }
  }
  return false;
}

/* ---------------------------------------------------------------- iterating */

pts_value pts_value_iterable(pts_value value, const pts_string *text) {
  if (pts_value_is_object(value)) {
    pts_object *object = pts_value_as_object(value);
    switch (object->type->kind) {
    case PTS_KIND_ARRAY:
    case PTS_KIND_TUPLE:
      return value; /* read live, as JS's array iterator does */
    case PTS_KIND_STRING:
      return pts_value_from_object(pts_array_from_string((pts_string *)object));
    case PTS_KIND_MAP:
    case PTS_KIND_SET: {
      pts_map *map = (pts_map *)object;
      bool set = object->type->kind == PTS_KIND_SET;
      pts_array *items = any_array();
      pts_map_iterator entries = pts_map_iterate(map);
      while (pts_map_next(&entries)) {
        pts_value key = pts_value_of_slot(map->type->key, &entries.key);
        if (set) {
          push_any(items, key);
          continue;
        }
        pts_value pair[] = {key, pts_value_of_slot(map->type->element, &entries.value)};
        push_any(items, pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_VALUE], pair, 2)));
      }
      return pts_value_from_object(items);
    }
    default:
      break;
    }
  }
  pts_throw_new(&pts_type_builtin_TypeError, "%.*s is not iterable", (int)text->byte_len, text->bytes);
}

double pts_value_iterable_length(pts_value iterable) { return (double)pts_spread_length(iterable); }

pts_value pts_value_iterable_element(pts_value iterable, double i) { return pts_spread_element(iterable, (size_t)i); }

pts_array *pts_value_to_array(pts_value value, const pts_string *text) {
  pts_value items = pts_value_iterable(value, text);
  pts_array *array = any_array();
  for (size_t i = 0, n = pts_spread_length(items); i < n; i++) {
    push_any(array, pts_spread_element(items, i));
  }
  return array;
}

/* ---------------------------------------------------------------- dispatch */

static builtin_method find(const named_method *methods, const char *name) {
  for (; methods->name != NULL; methods++) {
    if (strcmp(methods->name, name) == 0) {
      return methods->method;
    }
  }
  return NULL;
}

bool pts_builtin_method_call(pts_value value, const char *name, uint32_t argc, const pts_value *argv, pts_value *result) {
  const named_method *methods = primitive_methods; /* numbers and booleans */
  if (pts_value_is_object(value)) {
    switch (pts_value_as_object(value)->type->kind) {
    case PTS_KIND_STRING:
      methods = string_methods;
      break;
    case PTS_KIND_ARRAY:
      methods = array_methods;
      break;
    case PTS_KIND_FUNCTION:
      methods = function_methods;
      break;
    case PTS_KIND_MAP:
      methods = map_methods;
      break;
    case PTS_KIND_SET:
      methods = set_methods;
      break;
    case PTS_KIND_RECORD:
      methods = record_methods;
      break;
    case PTS_KIND_INT64:
    case PTS_KIND_UINT64:
      break;
    default:
      return false;
    }
  }
  builtin_method method = find(methods, name);
  if (method == NULL) {
    return false;
  }
  *result = method(value, argc, argv);
  return true;
}

bool pts_value_is_array(pts_value value) { return is_array_like(value); }
