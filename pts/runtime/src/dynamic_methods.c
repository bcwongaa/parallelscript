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
  return pts_value_from_object(pts_string_split_value(string_of(self), argument(argc, argv, 0), argument(argc, argv, 1)));
}

static pts_value string_match(pts_value self, uint32_t argc, const pts_value *argv) { return pts_string_match(string_of(self), argument(argc, argv, 0)); }

static pts_value string_match_all(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_string_match_all(string_of(self), argument(argc, argv, 0));
}

static pts_value string_normalize(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_string_normalize(string_of(self), argument(argc, argv, 0)));
}

static pts_value string_search(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(pts_string_search(string_of(self), argument(argc, argv, 0)));
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

/* replace and replaceAll of a RegExp with a callback: called on each match with its replacer arguments (D-134). */
static pts_value replace_regexp_with_callback(pts_value self, pts_regexp *regexp, pts_closure *callback, bool all) {
  pts_array *matches = pts_regexp_replace_matches(string_of(self), regexp, all);
  pts_array *replacements = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  for (size_t i = 0, n = pts_array_length_of(matches); i < n; i++) {
    pts_array *args = pts_regexp_replacer_arguments(pts_array_element(matches, (double)i));
    const pts_value *values = (const pts_value *)args->buffer->elements;
    pts_value replacement = pts_value_from_string(pts_value_to_string(pts_dynamic_call_closure(callback, (uint32_t)pts_array_length_of(args), values)));
    pts_array_push(replacements, &replacement, 1);
  }
  return pts_value_from_string(pts_regexp_replace_with(string_of(self), matches, replacements));
}

static pts_value string_replacing(pts_value self, uint32_t argc, const pts_value *argv, bool all) {
  pts_value pattern = argument(argc, argv, 0);
  pts_value replacement = argument(argc, argv, 1);
  if (is_function(replacement) && pts_value_is_object(pattern) && pts_value_as_object(pattern)->type->kind == PTS_KIND_REGEXP) {
    return replace_regexp_with_callback(self, (pts_regexp *)pts_value_as_object(pattern), (pts_closure *)pts_value_as_object(replacement), all);
  }
  if (is_function(replacement)) {
    return replace_with_callback(self, pts_value_to_string(pattern), (pts_closure *)pts_value_as_object(replacement), all);
  }
  return pts_value_from_string(pts_string_replace_value(string_of(self), pattern, pts_value_to_string(replacement), all));
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
  (void)argc, (void)argv;
  return pts_value_from_string(pts_value_to_string(self));
}

/* ---------------------------------------------------------------- numbers (D-083) */

static double number_self(pts_value self) { return pts_value_to_number(self); }

static pts_value number_to_string(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value radix = argument(argc, argv, 0);
  if (radix == PTS_VALUE_UNDEFINED) {
    return pts_value_from_string(pts_value_to_string(self));
  }
  double r = pts_value_to_number(radix);
  if (pts_value_is_object(self)) { /* a boxed 64-bit integer: every digit exact */
    pts_kind kind = pts_value_as_object(self)->type->kind;
    return pts_value_from_string(kind == PTS_KIND_INT64 ? pts_i64_to_string_radix(pts_value_as_i64(self), r)
                                                        : pts_u64_to_string_radix(pts_value_as_u64(self), r));
  }
  return pts_value_from_string(pts_number_to_string_radix(number_self(self), r));
}

static pts_value number_to_fixed(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_number_to_fixed(number_self(self), number_argument(argc, argv, 0, 0)));
}

static pts_value number_to_precision(pts_value self, uint32_t argc, const pts_value *argv) {
  if (argument(argc, argv, 0) == PTS_VALUE_UNDEFINED) {
    return pts_value_from_string(pts_value_to_string(self));
  }
  return pts_value_from_string(pts_number_to_precision(number_self(self), number_argument(argc, argv, 0, 0)));
}

static pts_value number_to_exponential(pts_value self, uint32_t argc, const pts_value *argv) {
  if (argument(argc, argv, 0) == PTS_VALUE_UNDEFINED) {
    return pts_value_from_string(pts_number_to_exponential_shortest(number_self(self)));
  }
  return pts_value_from_string(pts_number_to_exponential(number_self(self), number_argument(argc, argv, 0, 0)));
}

static pts_value number_to_locale_string(pts_value self, uint32_t argc, const pts_value *argv) {
  check_default_locale(argc, argv, 0, "toLocaleString");
  if (argument(argc, argv, 1) != PTS_VALUE_UNDEFINED) {
    pts_throw_new(&pts_type_builtin_TypeError, "toLocaleString with options through `any` is not supported yet");
  }
  return pts_value_from_string(pts_number_to_locale_string(number_self(self)));
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
    {"match", string_match},
    {"matchAll", string_match_all},
    {"normalize", string_normalize},
    {"padEnd", string_pad_end},
    {"padStart", string_pad_start},
    {"repeat", string_repeat},
    {"replace", string_replace},
    {"replaceAll", string_replace_all},
    {"search", string_search},
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

static const named_method number_methods[] = {
    {"toExponential", number_to_exponential},
    {"toFixed", number_to_fixed},
    {"toLocaleString", number_to_locale_string},
    {"toPrecision", number_to_precision},
    {"toString", number_to_string},
    {"valueOf", primitive_value_of},
    {NULL, NULL},
};

static const named_method boolean_methods[] = {
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
    bool holes = pts_value_as_object(part)->type->kind == PTS_KIND_ARRAY && pts_array_has_holes(array_of(part));
    for (size_t j = 0, length = pts_spread_length(part); j < length; j++) {
      bool hole = holes && !pts_array_has(array_of(part), (double)j); /* concat keeps holes: it defines only the indices that are there */
      push_any(result, hole ? PTS_VALUE_ABSENT : pts_spread_element(part, j));
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

/* The first index from `from` (relative) whose element equals value, by === (holes skipped, as indexOf only looks at the indices there are) or SameValueZero (holes read as undefined, as includes reads them); -1 if none. */
static double search_forward(pts_array *array, pts_value value, double from, bool zero) {
  double length = pts_array_length(array);
  bool skip_holes = !zero && value == PTS_VALUE_UNDEFINED; /* a hole reads as undefined, which no other value equals */
  for (double i = relative(from, length); i < pts_array_length(array); i++) {
    if (skip_holes && !pts_array_has(array, i)) {
      continue;
    }
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
  pts_value value = argument(argc, argv, 0);
  for (double i = from; i >= 0; i--) {
    if (pts_value_strict_equals(pts_array_element(array, i), value) && (value != PTS_VALUE_UNDEFINED || pts_array_has(array, i))) {
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

static pts_value array_flat(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_object(pts_array_flat_value(array_of(self), argc > 0 ? argv[0] : PTS_VALUE_UNDEFINED, &pts_array_types[PTS_FIELD_VALUE]));
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

/*
 * The callback methods but the find ones visit the indices below the length they start
 * with that the array still has (JS's HasProperty), skipping holes; a match's undefined
 * groups are elements (D-133). Those that visit every element look for holes once
 * (pts_array_has_holes), and test each index only if there are some: a hole that a
 * callback's spread puts into the array later is read as undefined.
 */
static bool has(const pts_array *array, bool holes, double index) {
  return holes ? pts_array_has(array, index) : index < pts_array_length(array);
}

static pts_value array_for_each(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  bool holes = pts_array_has_holes(array_of(self));
  for (double i = 0, length = pts_array_length(array_of(self)); i < length; i++) {
    if (has(array_of(self), holes, i)) {
      call_back(callback, self, i);
    }
  }
  return PTS_VALUE_UNDEFINED;
}

/* map's result has the array's length, with holes where the array has them. */
static pts_value array_map(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  double length = pts_array_length(array_of(self));
  bool holes = pts_array_has_holes(array_of(self));
  pts_array *result = pts_array_of_length(&pts_array_types[PTS_FIELD_VALUE], length);
  for (double i = 0; i < length; i++) {
    if (has(array_of(self), holes, i)) {
      pts_value mapped = call_back(callback, self, i);
      pts_array_set(result, i, &mapped);
    }
  }
  return pts_value_from_object(result);
}

static pts_value array_filter(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *array = array_of(self);
  pts_array *result = pts_array_of(pts_array_copy_type(array->type), NULL, 0); /* the same elements, so the same type */
  bool holes = pts_array_has_holes(array);
  for (double i = 0, length = pts_array_length(array); i < length; i++) {
    if (!has(array, holes, i)) {
      continue;
    }
    uint64_t element = 0;
    pts_array_load(array, i, &element); /* a match's undefined group stays a zero slot, undefined in a copy of a match too */
    if (pts_value_truthy(call_back(callback, self, i))) {
      pts_array_push(result, &element, 1);
    }
  }
  return pts_value_from_object(result);
}

static pts_value array_flat_map(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *result = any_array();
  for (double i = 0, length = pts_array_length(array_of(self)); i < length; i++) {
    if (pts_array_has(array_of(self), i)) { /* JS's flatMap visits the elements there are when it starts, skipping holes */
      pts_array_push_flat(result, call_back(callback, self, i));
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

/* Whether callback(element, index, array) is truthy (some) or falsy (every) for an element the array has. */
static bool any_element(pts_value self, uint32_t argc, const pts_value *argv, bool truthy) {
  pts_closure *callback = function_argument(argc, argv, 0);
  for (double i = 0, length = pts_array_length(array_of(self)); i < length; i++) {
    if (pts_array_has(array_of(self), i) && pts_value_truthy(call_back(callback, self, i)) == truthy) {
      return true;
    }
  }
  return false;
}

static pts_value array_some(pts_value self, uint32_t argc, const pts_value *argv) { return pts_value_from_bool(any_element(self, argc, argv, true)); }

static pts_value array_every(pts_value self, uint32_t argc, const pts_value *argv) { return pts_value_from_bool(!any_element(self, argc, argv, false)); }

/*
 * reduce and reduceRight: callback(accumulator, element, index, array) over the elements in
 * order or backwards, holes skipped; without an initial value the accumulator starts as
 * the first element there is, and TypeError if there is none.
 */
static pts_value reduce(pts_value self, uint32_t argc, const pts_value *argv, bool backwards) {
  pts_closure *callback = function_argument(argc, argv, 0);
  pts_array *array = array_of(self);
  double length = pts_array_length(array), n = 0;
  bool holes = pts_array_has_holes(array);
  pts_value accumulator = argc >= 2 ? argv[1] : PTS_VALUE_ABSENT;
  if (argc < 2) {
    while (n < length && !has(array, holes, backwards ? length - 1 - n : n)) {
      n++;
    }
    if (n == length) {
      pts_throw_new(&pts_type_builtin_TypeError, "Reduce of empty array with no initial value");
    }
    accumulator = pts_array_element(array, backwards ? length - 1 - n : n);
    n++;
  }
  for (; n < length; n++) {
    double i = backwards ? length - 1 - n : n;
    if (has(array, holes, i)) {
      pts_value args[] = {accumulator, pts_array_element(array, i), pts_value_from_number(i), self};
      accumulator = pts_dynamic_call_closure(callback, 4, args);
    }
  }
  return accumulator;
}

static pts_value array_reduce(pts_value self, uint32_t argc, const pts_value *argv) { return reduce(self, argc, argv, false); }

static pts_value array_reduce_right(pts_value self, uint32_t argc, const pts_value *argv) { return reduce(self, argc, argv, true); }

/*
 * Whether array's element type holds undefined (D-183): any[] and unknown[] (an array of
 * dynamic values a program doesn't describe), or a union with undefined. Copies read the
 * holes of those as undefined; a (string | null)[]'s keep them, since it can't hold one.
 */
static bool holds_undefined(const pts_array *array) {
  const pts_type *type = pts_unfrozen(array->type);
  pts_value converted;
  return type->element == PTS_FIELD_VALUE && (type->check == NULL || pts_value_try_convert(PTS_VALUE_UNDEFINED, type->check, &converted));
}

/* toSorted, toReversed, toSpliced and with (D-085): sort, reverse, splice and a write on a copy. */
static pts_value copy_of(pts_value self) {
  pts_array *array = array_of(self);
  return pts_value_from_object(holds_undefined(array) ? pts_array_copy_as_read(array) : pts_array_copy(array));
}

static pts_value array_to_sorted(pts_value self, uint32_t argc, const pts_value *argv) { return array_sort(copy_of(self), argc, argv); }

static pts_value array_to_reversed(pts_value self, uint32_t argc, const pts_value *argv) { return array_reverse(copy_of(self), argc, argv); }

static pts_value array_to_spliced(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value copy = copy_of(self);
  array_splice(copy, argc, argv);
  return copy;
}

/*
 * An array's values(), keys() and entries(): runtime iterators over what typed code's
 * iterate (D-110): the array itself, read live; its indices and its [index, element]
 * pairs as they are at the call, made then (JS's are live).
 */
static pts_value array_values(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_array_iterator_new(self);
}

static pts_value array_keys(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_array_iterator_new(pts_value_from_object(pts_array_keys(array_of(self))));
}

static pts_value array_entries(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  pts_array *array = array_of(self);
  pts_array *pairs = any_array();
  for (double i = 0; i < pts_array_length(array); i++) {
    pts_value pair[] = {pts_value_from_number(i), pts_value_present(pts_array_element(array, i))};
    push_any(pairs, pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_VALUE], pair, 2)));
  }
  return pts_array_iterator_new(pts_value_from_object(pairs));
}

static pts_value array_with(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_array *array = array_of(self);
  pts_value value = argument(argc, argv, 1);
  return pts_value_from_object(pts_array_with(array, number_argument(argc, argv, 0, 0), elements_for(array, 1, &value), holds_undefined(array)));
}

static const named_method array_methods[] = {
    {"at", array_at},
    {"concat", array_concat},
    {"entries", array_entries},
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
    {"keys", array_keys},
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
    {"toReversed", array_to_reversed},
    {"toSorted", array_to_sorted},
    {"toSpliced", array_to_spliced},
    {"toString", array_to_string},
    {"unshift", array_unshift},
    {"values", array_values},
    {"with", array_with},
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

/* A map's or set's values(), keys() and entries(): its live iterator (D-110). */
static pts_value map_values(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_map_iterator_new(self, 0);
}

static pts_value map_keys(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_map_iterator_new(self, 1);
}

static pts_value map_entries(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_map_iterator_new(self, 2);
}

static pts_value record_has_own_property(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_string *key = pts_value_to_string(argument(argc, argv, 0));
  return pts_value_from_bool(pts_map_has(map_of(self), &key));
}

static const named_method map_methods[] = {
    {"clear", map_clear},     {"delete", map_delete}, {"entries", map_entries}, {"forEach", map_for_each}, {"get", map_get},
    {"has", map_has},         {"keys", map_keys},     {"set", map_set},         {"values", map_values},   {NULL, NULL},
};

static const named_method set_methods[] = {
    {"add", set_add},         {"clear", map_clear},      {"delete", map_delete}, {"entries", map_entries}, {"forEach", map_for_each},
    {"has", map_has},         {"keys", map_keys},        {"values", map_values},   {NULL, NULL},
};

static const named_method record_methods[] = {
    {"hasOwnProperty", record_has_own_property},
    {NULL, NULL},
};

/* ---------------------------------------------------------------- dates (D-087) */

static pts_date *date_of(pts_value self) { return (pts_date *)pts_value_as_object(self); }

#define DATE_GETTER(name, field)                                                     \
  static pts_value name(pts_value self, uint32_t argc, const pts_value *argv) {      \
    (void)argc, (void)argv;                                                          \
    return pts_value_from_number(pts_date_get(date_of(self), field));                \
  }
#define DATE_SETTER(name, field)                                                     \
  static pts_value name(pts_value self, uint32_t argc, const pts_value *argv) {      \
    double a = number_argument(argc, argv, 0, __builtin_nan("")), b = number_argument(argc, argv, 1, 0), \
           c = number_argument(argc, argv, 2, 0), d = number_argument(argc, argv, 3, 0); \
    return pts_value_from_number(pts_date_set(date_of(self), field, argc, a, b, c, d)); \
  }
#define DATE_FORMAT(name, format)                                                    \
  static pts_value name(pts_value self, uint32_t argc, const pts_value *argv) {      \
    (void)argc, (void)argv;                                                          \
    return pts_value_from_string(pts_date_format(date_of(self), format));            \
  }
DATE_GETTER(date_get_time, PTS_DATE_TIME)
DATE_GETTER(date_get_timezone_offset, PTS_DATE_TIMEZONE_OFFSET)
DATE_GETTER(date_get_full_year, PTS_DATE_FULL_YEAR)
DATE_GETTER(date_get_month, PTS_DATE_MONTH)
DATE_GETTER(date_get_date, PTS_DATE_DATE)
DATE_GETTER(date_get_day, PTS_DATE_DAY)
DATE_GETTER(date_get_hours, PTS_DATE_HOURS)
DATE_GETTER(date_get_minutes, PTS_DATE_MINUTES)
DATE_GETTER(date_get_seconds, PTS_DATE_SECONDS)
DATE_GETTER(date_get_milliseconds, PTS_DATE_MILLISECONDS)
DATE_GETTER(date_get_utc_full_year, PTS_DATE_UTC_FULL_YEAR)
DATE_GETTER(date_get_utc_month, PTS_DATE_UTC_FULL_YEAR + 1)
DATE_GETTER(date_get_utc_date, PTS_DATE_UTC_FULL_YEAR + 2)
DATE_GETTER(date_get_utc_day, PTS_DATE_UTC_FULL_YEAR + 3)
DATE_GETTER(date_get_utc_hours, PTS_DATE_UTC_FULL_YEAR + 4)
DATE_GETTER(date_get_utc_minutes, PTS_DATE_UTC_FULL_YEAR + 5)
DATE_GETTER(date_get_utc_seconds, PTS_DATE_UTC_FULL_YEAR + 6)
DATE_GETTER(date_get_utc_milliseconds, PTS_DATE_UTC_FULL_YEAR + 7)
DATE_SETTER(date_set_full_year, PTS_DATE_FULL_YEAR)
DATE_SETTER(date_set_month, PTS_DATE_MONTH)
DATE_SETTER(date_set_date, PTS_DATE_DATE)
DATE_SETTER(date_set_hours, PTS_DATE_HOURS)
DATE_SETTER(date_set_minutes, PTS_DATE_MINUTES)
DATE_SETTER(date_set_seconds, PTS_DATE_SECONDS)
DATE_SETTER(date_set_milliseconds, PTS_DATE_MILLISECONDS)
DATE_SETTER(date_set_utc_full_year, PTS_DATE_UTC_FULL_YEAR)
DATE_SETTER(date_set_utc_month, PTS_DATE_UTC_FULL_YEAR + 1)
DATE_SETTER(date_set_utc_date, PTS_DATE_UTC_FULL_YEAR + 2)
DATE_SETTER(date_set_utc_hours, PTS_DATE_UTC_FULL_YEAR + 4)
DATE_SETTER(date_set_utc_minutes, PTS_DATE_UTC_FULL_YEAR + 5)
DATE_SETTER(date_set_utc_seconds, PTS_DATE_UTC_FULL_YEAR + 6)
DATE_SETTER(date_set_utc_milliseconds, PTS_DATE_UTC_FULL_YEAR + 7)
DATE_FORMAT(date_to_string, PTS_DATE_STRING)
DATE_FORMAT(date_to_date_string, PTS_DATE_DATE_ONLY)
DATE_FORMAT(date_to_time_string, PTS_DATE_TIME_ONLY)
DATE_FORMAT(date_to_iso_string, PTS_DATE_ISO)
DATE_FORMAT(date_to_utc_string, PTS_DATE_UTC)
#undef DATE_GETTER
#undef DATE_SETTER
#undef DATE_FORMAT

static pts_value date_set_time(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_number(pts_date_set_time(date_of(self), number_argument(argc, argv, 0, __builtin_nan(""))));
}

static pts_value date_to_json(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_date_to_json(date_of(self));
}

static const named_method date_methods[] = {
    {"getDate", date_get_date}, {"getDay", date_get_day}, {"getFullYear", date_get_full_year}, {"getHours", date_get_hours},
    {"getMilliseconds", date_get_milliseconds}, {"getMinutes", date_get_minutes}, {"getMonth", date_get_month},
    {"getSeconds", date_get_seconds}, {"getTime", date_get_time}, {"getTimezoneOffset", date_get_timezone_offset},
    {"getUTCDate", date_get_utc_date}, {"getUTCDay", date_get_utc_day}, {"getUTCFullYear", date_get_utc_full_year},
    {"getUTCHours", date_get_utc_hours}, {"getUTCMilliseconds", date_get_utc_milliseconds}, {"getUTCMinutes", date_get_utc_minutes},
    {"getUTCMonth", date_get_utc_month}, {"getUTCSeconds", date_get_utc_seconds}, {"setDate", date_set_date},
    {"setFullYear", date_set_full_year}, {"setHours", date_set_hours}, {"setMilliseconds", date_set_milliseconds},
    {"setMinutes", date_set_minutes}, {"setMonth", date_set_month}, {"setSeconds", date_set_seconds}, {"setTime", date_set_time},
    {"setUTCDate", date_set_utc_date}, {"setUTCFullYear", date_set_utc_full_year}, {"setUTCHours", date_set_utc_hours},
    {"setUTCMilliseconds", date_set_utc_milliseconds}, {"setUTCMinutes", date_set_utc_minutes}, {"setUTCMonth", date_set_utc_month},
    {"setUTCSeconds", date_set_utc_seconds}, {"toDateString", date_to_date_string}, {"toGMTString", date_to_utc_string},
    {"toISOString", date_to_iso_string}, {"toJSON", date_to_json}, {"toString", date_to_string}, {"toTimeString", date_to_time_string},
    {"toUTCString", date_to_utc_string}, {"valueOf", date_get_time}, {NULL, NULL},
};

/* The methods of typed arrays that take no callbacks, through `any` (D-125). */
static pts_typed_array *typed_array_of(pts_value self) { return (pts_typed_array *)pts_value_as_object(self); }

static pts_value typed_array_value(pts_typed_array *array) { return pts_value_from_object(array); }

static pts_value typed_array_at(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_typed_array_at(typed_array_of(self), number_argument(argc, argv, 0, 0));
}

static pts_value typed_array_fill(pts_value self, uint32_t argc, const pts_value *argv) {
  return typed_array_value(pts_typed_array_fill(typed_array_of(self), number_argument(argc, argv, 0, __builtin_nan("")),
                                                number_argument(argc, argv, 1, 0), number_argument(argc, argv, 2, __builtin_inf())));
}

static pts_value typed_array_includes(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value search = argument(argc, argv, 0);
  return pts_value_from_bool(pts_value_is_number(search) && pts_typed_array_includes(typed_array_of(self), pts_value_as_number(search), number_argument(argc, argv, 1, 0)));
}

static pts_value typed_array_index_of(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value search = argument(argc, argv, 0);
  return pts_value_from_number(pts_value_is_number(search) ? pts_typed_array_index_of(typed_array_of(self), pts_value_as_number(search), number_argument(argc, argv, 1, 0)) : -1);
}

static pts_value typed_array_last_index_of(pts_value self, uint32_t argc, const pts_value *argv) {
  pts_value search = argument(argc, argv, 0);
  return pts_value_from_number(pts_value_is_number(search) ? pts_typed_array_last_index_of(typed_array_of(self), pts_value_as_number(search), number_argument(argc, argv, 1, __builtin_inf())) : -1);
}

static pts_value typed_array_join(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_string(pts_typed_array_join(typed_array_of(self), string_argument(argc, argv, 0, NULL)));
}

static pts_value typed_array_to_string(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_value_from_string(pts_typed_array_join(typed_array_of(self), NULL));
}

static pts_value typed_array_reverse(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return typed_array_value(pts_typed_array_reverse(typed_array_of(self)));
}

static pts_value typed_array_slice(pts_value self, uint32_t argc, const pts_value *argv) {
  return typed_array_value(pts_typed_array_slice(typed_array_of(self), number_argument(argc, argv, 0, 0), number_argument(argc, argv, 1, __builtin_inf())));
}

static pts_value typed_array_subarray(pts_value self, uint32_t argc, const pts_value *argv) {
  return typed_array_value(pts_typed_array_subarray(typed_array_of(self), number_argument(argc, argv, 0, 0), number_argument(argc, argv, 1, __builtin_inf())));
}

static pts_value typed_array_sort(pts_value self, uint32_t argc, const pts_value *argv) {
  if (argument(argc, argv, 0) != PTS_VALUE_UNDEFINED) {
    pts_throw_new(&pts_type_builtin_TypeError, "Sorting a typed array with a comparator through `any` is not supported yet");
  }
  return typed_array_value(pts_typed_array_sort(typed_array_of(self)));
}

static const named_method typed_array_methods[] = {
    {"at", typed_array_at},         {"fill", typed_array_fill},           {"includes", typed_array_includes},
    {"indexOf", typed_array_index_of}, {"join", typed_array_join},        {"lastIndexOf", typed_array_last_index_of},
    {"reverse", typed_array_reverse}, {"slice", typed_array_slice},       {"sort", typed_array_sort},
    {"subarray", typed_array_subarray}, {"toString", typed_array_to_string}, {NULL, NULL},
};

/* ---------------------------------------------------------------- RegExp (D-133) */

static pts_regexp *regexp_of(pts_value self) { return (pts_regexp *)pts_value_as_object(self); }

static pts_value regexp_exec(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_regexp_exec(regexp_of(self), string_argument(argc, argv, 0, NULL));
}

static pts_value regexp_test(pts_value self, uint32_t argc, const pts_value *argv) {
  return pts_value_from_bool(pts_regexp_test(regexp_of(self), string_argument(argc, argv, 0, NULL)));
}

static pts_value regexp_to_string(pts_value self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_value_from_string(pts_regexp_to_string(regexp_of(self)));
}

static const named_method regexp_methods[] = {
    {"exec", regexp_exec}, {"test", regexp_test}, {"toString", regexp_to_string}, {NULL, NULL},
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
  case PTS_KIND_TUPLE:
  case PTS_KIND_TYPED_ARRAY: {
    double length = object->type->kind == PTS_KIND_STRING ? ((pts_string *)object)->utf16_len : (double)pts_spread_length(value);
    for (double i = 0; i < length; i++) {
      if (object->type->kind == PTS_KIND_ARRAY && !pts_array_has((const pts_array *)object, i)) {
        continue; /* a hole isn't a property */
      }
      pts_string *index = pts_number_to_string(i);
      pts_array_push(keys, &index, 1);
    }
    if (pts_is_match_array(object)) {
      static const char *const match_keys[] = {"index", "input", "groups"};
      for (size_t i = 0; i < sizeof match_keys / sizeof match_keys[0]; i++) {
        pts_string *name = pts_string_from_cstr(match_keys[i]);
        pts_array_push(keys, &name, 1);
      }
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
    if (field->name[0] == '#' || pts_name_is_symbol(field->name) || field->offset < first ||
        (!pts_field_is_accessor(field->kind) && field->kind == PTS_FIELD_VALUE &&
         __atomic_load_n((const pts_value *)((const char *)object + field->offset), __ATOMIC_RELAXED) == PTS_VALUE_ABSENT)) {
      continue; /* private, a symbol's (D-111), an error's, or an optional property left out */
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

pts_array *pts_value_key_array(pts_value value) { return own_keys(value); }

void pts_object_assign(pts_value target, pts_array *sources) {
  if (pts_value_is_nullish(target)) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot convert undefined or null to object");
  }
  for (double s = 0; s < pts_array_length(sources); s++) {
    pts_value source = pts_array_element(sources, s);
    if (pts_value_is_nullish(source)) {
      continue; /* JS skips them */
    }
    pts_array *keys = own_keys(source);
    for (double i = 0; i < pts_array_length(keys); i++) {
      pts_value key = pts_array_element(keys, i);
      pts_dynamic_set(target, key, pts_dynamic_get(source, key));
    }
  }
}

bool pts_value_same_value(pts_value a, pts_value b) {
  if (pts_value_is_number(a) && pts_value_is_number(b)) {
    double x = pts_value_as_number(a), y = pts_value_as_number(b);
    return x != x ? y != y : x == y && __builtin_signbit(x) == __builtin_signbit(y);
  }
  return pts_value_strict_equals(a, b);
}

/* The builtin error class an error's class is or extends. */
static const pts_type *builtin_error_type(const pts_object *error) {
  static const pts_type *const builtins[] = {&pts_type_builtin_Error,          &pts_type_builtin_TypeError,
                                             &pts_type_builtin_RangeError,     &pts_type_builtin_ReferenceError,
                                             &pts_type_builtin_SyntaxError,    &pts_type_builtin_EvalError,
                                             &pts_type_builtin_URIError};
  for (const pts_type *t = error->type; t != NULL; t = t->parent) {
    for (size_t i = 0; i < sizeof builtins / sizeof *builtins; i++) {
      if (t == builtins[i]) {
        return t;
      }
    }
  }
  return &pts_type_builtin_Error;
}

/* Whether a string's text is the C string text. */
static bool string_is(const pts_string *string, const char *text) {
  size_t len = strlen(text);
  return string->byte_len == len && memcmp(string->bytes, text, len) == 0;
}

/* Whether value has the own property key that isn't enumerable: an array's or string's
   length; an error's stack, message (when not empty), cause (when given) and name (when
   its constructor or a field set it: when it isn't its builtin class's). */
static bool has_hidden_own(pts_value value, const pts_string *key) {
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_object *object = pts_value_as_object(value);
  switch (object->type->kind) {
  case PTS_KIND_STRING:
  case PTS_KIND_ARRAY:
  case PTS_KIND_TUPLE:
    return string_is(key, "length");
  case PTS_KIND_OBJECT:
    break;
  default:
    return false;
  }
  if (!pts_instance_of(object, &pts_type_builtin_Error)) {
    return false;
  }
  pts_error *error = (pts_error *)object;
  if (string_is(key, "stack")) {
    return true;
  }
  if (string_is(key, "message")) {
    const pts_string *message = error->message; /* once (D-143) */
    return message != NULL && message->utf16_len > 0;
  }
  if (string_is(key, "cause")) {
    return error->cause != PTS_VALUE_ABSENT;
  }
  if (string_is(key, "name")) {
    const char *builtin = builtin_error_type(object)->name;
    const pts_string *name = error->name; /* once (D-143) */
    return name != NULL && !string_is(name, builtin);
  }
  return false;
}

bool pts_value_has_own(pts_value value, pts_value key) {
  pts_array *keys = own_keys(value);
  pts_value name = pts_value_from_string(pts_value_to_string(key));
  if (has_hidden_own(value, pts_value_as_string(name))) {
    return true;
  }
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
    case PTS_KIND_TYPED_ARRAY:
      return pts_value_from_object(pts_typed_array_to_array((pts_typed_array *)object));
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
    case PTS_KIND_GENERATOR:
      return pts_value_from_object(pts_iterator_to_array(value)); /* what it yields until it is done */
    case PTS_KIND_OBJECT: {
      pts_value method = pts_iterator_method(value);
      if (pts_value_is_object(method) && pts_value_as_object(method)->type->kind == PTS_KIND_FUNCTION) {
        return pts_value_from_object(pts_iterator_to_array(pts_iterator_of(pts_dynamic_call(method, text, 0, NULL))));
      }
      break; /* none, or no function: not iterable */
    }
    default:
      break;
    }
  }
  pts_throw_new(&pts_type_builtin_TypeError, "%.*s is not iterable", (int)text->byte_len, text->bytes);
}

pts_array *pts_value_to_array(pts_value value, const pts_string *text) {
  pts_value items = pts_value_iterable(value, text);
  size_t n = pts_spread_length(items);
  pts_array *array = pts_array_of_length(&pts_array_types[PTS_FIELD_VALUE], (double)n);
  pts_value *slots = (pts_value *)(void *)array->buffer->elements; /* a new array only we see */
  for (size_t i = 0; i < n; i++) {
    slots[i] = pts_spread_element(items, i);
  }
  return array;
}

pts_value pts_value_spread_items(pts_value value, const pts_string *text) {
  if (pts_value_is_object(value)) {
    pts_kind kind = pts_value_as_object(value)->type->kind;
    if (kind == PTS_KIND_ARRAY || kind == PTS_KIND_TUPLE || kind == PTS_KIND_TYPED_ARRAY) {
      return value; /* spread_arguments spreads these at the call */
    }
  }
  return pts_value_iterable(value, text); /* anything else's items in a new array, of any element kind */
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

/* The builtin methods of value's kind; NULL for a generator's (found apart) and for the kinds that have none. */
static const named_method *builtin_methods(pts_value value) {
  if (!pts_value_is_object(value)) {
    return value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE ? boolean_methods : number_methods;
  }
  switch (pts_value_as_object(value)->type->kind) {
  case PTS_KIND_STRING:
    return string_methods;
  case PTS_KIND_ARRAY:
    return array_methods;
  case PTS_KIND_FUNCTION:
    return function_methods;
  case PTS_KIND_MAP:
    return map_methods;
  case PTS_KIND_SET:
    return set_methods;
  case PTS_KIND_RECORD:
    return record_methods;
  case PTS_KIND_DATE:
    return date_methods;
  case PTS_KIND_TYPED_ARRAY:
    return typed_array_methods;
  case PTS_KIND_REGEXP:
    return regexp_methods;
  case PTS_KIND_INT64:
  case PTS_KIND_UINT64:
    return number_methods;
  default:
    return NULL;
  }
}

static bool is_generator(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_GENERATOR;
}

bool pts_builtin_method_call(pts_value value, const char *name, uint32_t argc, const pts_value *argv, pts_value *result) {
  if (is_generator(value)) {
    return pts_iterator_method_call(value, name, argc, argv, result);
  }
  const named_method *methods = builtin_methods(value);
  builtin_method method = methods == NULL ? NULL : find(methods, name);
  if (method == NULL) {
    return false;
  }
  *result = method(value, argc, argv);
  return true;
}

bool pts_builtin_method_has(pts_value value, const char *name) {
  if (is_generator(value)) {
    return pts_iterator_has_method(value, name);
  }
  const named_method *methods = builtin_methods(value);
  return methods != NULL && find(methods, name) != NULL;
}

bool pts_builtin_method_named(pts_value value, const pts_string *name) {
  if (name->byte_len == 0) {
    return false;
  }
  if (is_generator(value)) {
    char buffer[16]; /* longer than any generator method's name */
    if (name->byte_len >= sizeof buffer || memchr(name->bytes, '\0', name->byte_len) != NULL) {
      return false; /* a NUL would cut the name short (`"next\0"`, H10b's R8-F1) */
    }
    memcpy(buffer, name->bytes, name->byte_len);
    buffer[name->byte_len] = '\0';
    return pts_iterator_has_method(value, buffer);
  }
  const named_method *methods = builtin_methods(value);
  for (; methods != NULL && methods->name != NULL; methods++) {
    if (methods->name[0] == name->bytes[0] && strlen(methods->name) == name->byte_len && memcmp(methods->name, name->bytes, name->byte_len) == 0) {
      return true;
    }
  }
  return false;
}

bool pts_value_is_array(pts_value value) { return is_array_like(value); }
