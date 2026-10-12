/*
 * console.log's formatting: a port of Node's util.inspect and util.format
 * (lib/internal/util/inspect.js) with their defaults (D-O16): depth 2, breakLength 80,
 * compact 3, maxArrayLength 100, maxStringLength 10000. Objects are read through their
 * type descriptors (D-024); format strings (%s, %d, %o, …) follow formatWithOptions (D-088).
 */
#include "inspect_internal.h"

#include <inttypes.h>
#include <math.h>
#include <setjmp.h>
#include <stdio.h>
#include <stdlib.h>

#include "width_tables.h"

enum { DEPTH = 2, BREAK_LENGTH = 80, MAX_ARRAY_LENGTH = 100, MAX_STRING_LENGTH = 10000, MIN_LINE_WIDTH = 16 };

/* The JS .length of UTF-8 text: code points, astral ones counting twice. */
static size_t utf16_length(const char *bytes, size_t len) {
  size_t units = 0;
  for (size_t i = 0; i < len; i++) {
    unsigned char byte = (unsigned char)bytes[i];
    if ((byte & 0xC0) != 0x80) {
      units += byte >= 0xF0 ? 2 : 1;
    }
  }
  return units;
}

typedef struct inspector {
  const pts_object **seen; /* the objects being formatted, outermost first */
  size_t seen_len, seen_cap;
  const pts_object **circular; /* targets of circular references; reference n is circular[n-1] */
  size_t circular_len, circular_cap;
  size_t indentation;   /* Node's ctx.indentationLvl */
  size_t current_depth; /* Node's ctx.currentDepth */
  double depth;         /* Node's ctx.depth: how many levels of objects to show; INFINITY for null */
  bool show_hidden;     /* Node's ctx.showHidden: non-enumerable properties too */
  double break_length;  /* Node's ctx.breakLength: the columns a line may take; INFINITY for one line */
  size_t max_array_length; /* Node's ctx.maxArrayLength: the elements of an array, map or set shown */
} inspector;

static inspector new_inspector(double depth, bool show_hidden) {
  return (inspector){.depth = depth, .show_hidden = show_hidden, .break_length = BREAK_LENGTH, .max_array_length = MAX_ARRAY_LENGTH};
}

static void free_inspector(inspector *in) {
  free(in->seen);
  free(in->circular);
}

/* Whether objects at recurse_times levels down are beyond the depth, so they show as [Name]. */
static bool beyond_depth(const inspector *in, size_t recurse_times) { return (double)recurse_times > in->depth; }

static void push(const pts_object ***items, size_t *len, size_t *cap, const pts_object *item) {
  if (*len == *cap) {
    *cap = *cap ? *cap * 2 : 8;
    *items = realloc(*items, *cap * sizeof **items);
    if (*items == NULL) {
      pts_panic("RangeError", "Out of memory formatting a value for console.log");
    }
  }
  (*items)[(*len)++] = item;
}

/* Node's meta table: how strEscape writes each character below 0xA0 that needs escaping; hex holds 8 bytes. */
static const char *escape_of(uint32_t c, char *hex) {
  switch (c) {
  case '\b':
    return "\\b";
  case '\t':
    return "\\t";
  case '\n':
    return "\\n";
  case '\f':
    return "\\f";
  case '\r':
    return "\\r";
  case '\'':
    return "\\'";
  case '\\':
    return "\\\\";
  }
  snprintf(hex, 8, "\\x%02X", c);
  return hex;
}

/* Decodes one WTF-8 sequence (lone surrogates included) at bytes, setting *size. */
static uint32_t decode(const unsigned char *bytes, size_t *size) {
  if (bytes[0] < 0x80) {
    *size = 1;
    return bytes[0];
  }
  if (bytes[0] < 0xE0) {
    *size = 2;
    return (uint32_t)(bytes[0] & 0x1F) << 6 | (bytes[1] & 0x3F);
  }
  if (bytes[0] < 0xF0) {
    *size = 3;
    return (uint32_t)(bytes[0] & 0x0F) << 12 | (uint32_t)(bytes[1] & 0x3F) << 6 | (bytes[2] & 0x3F);
  }
  *size = 4;
  return (uint32_t)(bytes[0] & 0x07) << 18 | (uint32_t)(bytes[1] & 0x3F) << 12 | (uint32_t)(bytes[2] & 0x3F) << 6 | (bytes[3] & 0x3F);
}

/*
 * Node's strEscape: single quotes, unless the string has one and no double quote
 * (then double quotes) or has both but no backtick or "${" (then backticks).
 * Control characters, the quote, backslashes and lone surrogates are escaped.
 */
static void quote_bytes(text *out, const char *bytes, size_t len) {
  char quote = '\'';
  if (memchr(bytes, '\'', len) != NULL) {
    if (memchr(bytes, '"', len) == NULL) {
      quote = '"';
    } else if (memchr(bytes, '`', len) == NULL && memmem(bytes, len, "${", 2) == NULL) {
      quote = '`';
    }
  }
  text_add(out, &quote, 1);
  size_t last = 0;
  for (size_t i = 0; i < len;) {
    size_t size;
    uint32_t c = decode((const unsigned char *)bytes + i, &size);
    const char *escape = NULL;
    char buf[8];
    if ((c == '\'' && quote == '\'') || c == '\\' || c < 0x20 || (c >= 0x7F && c < 0xA0)) {
      escape = escape_of(c, buf);
    } else if (c >= 0xD800 && c <= 0xDFFF) { /* a lone surrogate: pairs are 4-byte sequences */
      snprintf(buf, sizeof buf, "\\u%x", c);
      escape = buf;
    }
    if (escape != NULL) {
      text_add(out, bytes + last, i - last);
      text_puts(out, escape);
      last = i + size;
    }
    i += size;
  }
  text_add(out, bytes + last, len - last);
  text_add(out, &quote, 1);
}

static void spaces(text *out, size_t count);

static void quote_string(text *out, const pts_string *string) { quote_bytes(out, string->bytes, string->byte_len); }

/*
 * Node's formatPrimitive for a string: quoted, cut after MAX_STRING_LENGTH UTF-16 units
 * ("... n more characters"), and, if longer than a line has room for, split after each
 * line break into quoted pieces joined by " +" and a line break.
 */
static void format_string(inspector *in, text *out, pts_string *string) {
  size_t units = string->utf16_len;
  size_t remaining = 0;
  if (units > MAX_STRING_LENGTH) {
    remaining = units - MAX_STRING_LENGTH;
    units = MAX_STRING_LENGTH;
    string = pts_string_slice(string, 0, MAX_STRING_LENGTH);
  }
  const char *bytes = string->bytes;
  size_t len = string->byte_len;
  if (units > MIN_LINE_WIDTH && (double)units > in->break_length - (double)in->indentation - 4) {
    size_t start = 0;
    for (const char *newline; start < len && (newline = memchr(bytes + start, '\n', len - start)) != NULL;) {
      size_t end = (size_t)(newline - bytes) + 1;
      quote_bytes(out, bytes + start, end - start);
      start = end;
      if (start < len) {
        text_puts(out, " +\n");
        spaces(out, in->indentation + 2);
      }
    }
    if (start < len) {
      quote_bytes(out, bytes + start, len - start);
    }
  } else {
    quote_bytes(out, bytes, len);
  }
  if (remaining > 0) {
    text_printf(out, "... %zu more character%s", remaining, remaining > 1 ? "s" : "");
  }
}

static void format_number(text *out, double number) {
  if (number == 0 && __builtin_signbit(number)) {
    text_puts(out, "-0");
    return;
  }
  char buf[PTS_NUMBER_BUF_SIZE];
  text_add(out, buf, pts_number_format(number, buf));
}

static void format_heap_object(inspector *in, text *out, const pts_object *object, size_t recurse_times);
static void format_promise(inspector *in, text *out, const pts_object *promise, size_t recurse_times);
static void format_typed_array(inspector *in, text *out, const pts_typed_array *array, size_t recurse_times);
static void format_array_buffer(inspector *in, text *out, const pts_arraybuffer *buffer, size_t recurse_times, bool contents);
static void format_error(inspector *in, text *out, pts_error *error, size_t recurse_times);

const pts_type pts_bound_function = {.kind = PTS_KIND_FUNCTION, .name = "bound"};
const pts_type pts_generator_function = {.kind = PTS_KIND_FUNCTION, .name = "GeneratorFunction"};
const pts_type pts_async_function = {.kind = PTS_KIND_FUNCTION, .name = "AsyncFunction"};
const pts_type pts_async_generator_function = {.kind = PTS_KIND_FUNCTION, .name = "AsyncGeneratorFunction"};

/*
 * Node's formatting of a function without properties: `[Function: name]`, or `[Function
 * (anonymous)]`. An adapter shows as the function it adapts, and a bound function as
 * "bound " and the name of the function it binds; both capture that function first.
 */
/* The closure that closure wraps, following adapters and bound functions (whose descriptors have no name), and whether one of them was a bound function. */
static const pts_closure *wrapped_function(const pts_closure *closure, size_t *bound) {
  while (closure->type->name == NULL) {
    if (closure->type->parent == &pts_bound_function && bound != NULL) {
      (*bound)++;
    }
    closure = ((const pts_closure *const *)(closure + 1))[0];
  }
  return closure;
}

pts_string *pts_value_function_name(pts_value function) {
  size_t bound = 0;
  const pts_closure *closure = wrapped_function((const pts_closure *)pts_value_as_object(function), &bound);
  text name = {0};
  for (size_t i = 0; i < bound; i++) {
    text_puts(&name, "bound ");
  }
  text_puts(&name, closure->type->name);
  pts_string *result = pts_string_from_utf8(name.bytes == NULL ? "" : name.bytes, name.len);
  free(name.bytes);
  return result;
}

double pts_value_function_length(pts_value function) {
  return wrapped_function((const pts_closure *)pts_value_as_object(function), NULL)->type->length;
}

static void format_function(text *out, const pts_closure *closure) {
  text name = {0};
  while (closure->type->name == NULL) {
    if (closure->type->parent == &pts_bound_function) {
      text_puts(&name, "bound ");
    }
    closure = ((const pts_closure *const *)(closure + 1))[0];
  }
  text_puts(&name, closure->type->name);
  const char *kind = closure->type->parent != NULL && closure->type->parent != &pts_bound_function ? closure->type->parent->name : "Function";
  if (name.len == 0) {
    text_printf(out, "[%s (anonymous)]", kind);
  } else {
    text_printf(out, "[%s: %.*s]", kind, (int)name.len, name.bytes);
  }
  free(name.bytes);
}

/* Node's formatValue for a value nested in an object or array: strings are quoted. */
static void format_value(inspector *in, text *out, pts_value value, size_t recurse_times) {
  if (pts_value_is_number(value)) {
    format_number(out, pts_value_as_number(value));
    return;
  }
  if (!pts_value_is_object(value)) {
    switch (value) {
    case PTS_VALUE_UNDEFINED:
      text_puts(out, "undefined");
      return;
    case PTS_VALUE_NULL:
      text_puts(out, "null");
      return;
    case PTS_VALUE_TRUE:
      text_puts(out, "true");
      return;
    case PTS_VALUE_FALSE:
      text_puts(out, "false");
      return;
    }
    pts_panic("Error", "console.log got a malformed value 0x%llx", (unsigned long long)value);
  }
  const pts_object *object = pts_value_as_object(value);
  switch (object->type->kind) {
  case PTS_KIND_STRING:
    format_string(in, out, (pts_string *)object);
    return;
  case PTS_KIND_INT64:
    text_printf(out, "%" PRId64, ((const pts_boxed_int64 *)object)->value);
    return;
  case PTS_KIND_UINT64:
    text_printf(out, "%" PRIu64, ((const pts_boxed_uint64 *)object)->value);
    return;
  case PTS_KIND_OBJECT:
  case PTS_KIND_ARRAY:
  case PTS_KIND_TUPLE:
  case PTS_KIND_MAP:
  case PTS_KIND_SET:
  case PTS_KIND_RECORD:
    format_heap_object(in, out, object, recurse_times);
    return;
  case PTS_KIND_FUNCTION:
    format_function(out, (const pts_closure *)object);
    return;
  case PTS_KIND_DATE: {
    double t = ((pts_date *)object)->time; /* once: another thread may set it meanwhile */
    pts_string *text = pts_time_format(t, t != t ? PTS_DATE_STRING : PTS_DATE_ISO);
    text_add(out, text->bytes, text->byte_len);
    return;
  }
  case PTS_KIND_PROMISE:
    format_promise(in, out, object, recurse_times);
    return;
  case PTS_KIND_TYPED_ARRAY:
    format_typed_array(in, out, (const pts_typed_array *)object, recurse_times);
    return;
  case PTS_KIND_ARRAY_BUFFER:
    format_array_buffer(in, out, (const pts_arraybuffer *)object, recurse_times, true);
    return;
  case PTS_KIND_REGEXP: {
    pts_string *text = pts_regexp_to_string((const pts_regexp *)object);
    text_add(out, text->bytes, text->byte_len);
    return;
  }
  case PTS_KIND_GENERATOR: {
    pts_array *items;
    bool entries;
    if (!pts_map_iterator_remaining(object, &items, &entries)) {
      text_printf(out, "Object [%s] {}", object->type->name);
      return;
    }
    /* Node's formatIterator: the items left, as a map's or set's, `[Map Entries]` for entries */
    text_printf(out, "[%.3s %s] { ", object->type->name, entries ? "Entries" : "Iterator");
    size_t n = pts_array_length_of(items);
    for (size_t i = 0; i < n; i++) {
      if (i > 0) {
        text_puts(out, ", ");
      }
      format_value(in, out, pts_array_element(items, (double)i), recurse_times + 1);
    }
    text_puts(out, " }");
    return;
  }
  }
}

/* format_slot formats the value of the given kind at slot: a field of an object or an element of an array. */
static void format_slot(inspector *in, text *out, pts_field_kind kind, const void *slot, size_t recurse_times) {
#define SLOT(type) (*(const type *)slot)
  switch (kind) {
  case PTS_FIELD_NUMBER:
    format_number(out, SLOT(double));
    return;
  case PTS_FIELD_F32:
    format_number(out, SLOT(float));
    return;
  case PTS_FIELD_BOOL:
    text_puts(out, SLOT(bool) ? "true" : "false");
    return;
  case PTS_FIELD_STRING: { /* each slot loaded once: another thread may change it between a test and a use (D-143) */
    pts_string *string = __atomic_load_n((pts_string *const *)slot, __ATOMIC_RELAXED);
    if (string == NULL) {
      text_puts(out, "undefined"); /* a hole that threads racing on an array left */
    } else {
      format_string(in, out, string);
    }
    return;
  }
  case PTS_FIELD_VALUE: {
    pts_value value = __atomic_load_n((const pts_value *)slot, __ATOMIC_RELAXED);
    format_value(in, out, value == 0 ? PTS_VALUE_UNDEFINED : value, recurse_times);
    return;
  }
  case PTS_FIELD_OBJECT: {
    pts_object *object = __atomic_load_n((pts_object *const *)slot, __ATOMIC_RELAXED);
    if (object == NULL) {
      text_puts(out, "undefined"); /* a field read before the constructor assigned it */
    } else {
      format_value(in, out, pts_value_from_object(object), recurse_times);
    }
    return;
  }
  case PTS_FIELD_I8:
    text_printf(out, "%d", SLOT(int8_t));
    return;
  case PTS_FIELD_I16:
    text_printf(out, "%d", SLOT(int16_t));
    return;
  case PTS_FIELD_I32:
    text_printf(out, "%" PRId32, SLOT(int32_t));
    return;
  case PTS_FIELD_I64:
    text_printf(out, "%" PRId64, SLOT(int64_t));
    return;
  case PTS_FIELD_U8:
    text_printf(out, "%u", SLOT(uint8_t));
    return;
  case PTS_FIELD_U16:
    text_printf(out, "%u", SLOT(uint16_t));
    return;
  case PTS_FIELD_U32:
    text_printf(out, "%" PRIu32, SLOT(uint32_t));
    return;
  case PTS_FIELD_U64:
    text_printf(out, "%" PRIu64, SLOT(uint64_t));
    return;
  case PTS_FIELD_GETTER:
    text_puts(out, "[Getter]");
    return;
  case PTS_FIELD_SETTER:
    text_puts(out, "[Setter]");
    return;
  case PTS_FIELD_GETTER_SETTER:
    text_puts(out, "[Getter/Setter]");
    return;
  }
#undef SLOT
}

/* is_bare_key reports whether a key matches Node's keyStrRegExp, ^[a-zA-Z_][a-zA-Z_0-9]*$, so it prints unquoted. */
static bool is_bare_key(const char *bytes, size_t len) {
  bool bare = len > 0 && !(bytes[0] >= '0' && bytes[0] <= '9');
  for (size_t i = 0; i < len && bare; i++) {
    char c = bytes[i];
    bare = c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9');
  }
  return bare;
}

/* A field's name prints bare if is_bare_key, else quoted. */
static void format_key(text *out, const char *name) {
  if (is_bare_key(name, strlen(name))) {
    text_puts(out, name);
    return;
  }
  pts_string *key = pts_string_from_cstr(name);
  quote_string(out, key);
}

/* format_string_key is format_key for a hash-map object's key. An own `__proto__` key, which JSON.parse makes, shows as Node shows it. */
static void format_string_key(text *out, const pts_string *key) {
  if (key->byte_len == 9 && memcmp(key->bytes, "__proto__", 9) == 0) {
    text_puts(out, "['__proto__']");
    return;
  }
  if (is_bare_key(key->bytes, key->byte_len)) {
    text_add(out, key->bytes, key->byte_len);
    return;
  }
  quote_string(out, key);
}

static size_t circular_index(inspector *in, const pts_object *object) {
  for (size_t i = 0; i < in->circular_len; i++) {
    if (in->circular[i] == object) {
      return i + 1;
    }
  }
  return 0;
}

/* Node's isBelowBreakLength: whether the entries fit on one line after start columns (its caller checks for line breaks). */
static bool below_break_length(const inspector *in, text *entries, size_t count, size_t start) {
  size_t total = count + start;
  if ((double)(total + count) > in->break_length) {
    return false;
  }
  for (size_t i = 0; i < count; i++) {
    total += utf16_length(entries[i].bytes, entries[i].len);
    if ((double)total > in->break_length) {
      return false;
    }
  }
  return true;
}

static void spaces(text *out, size_t count) {
  for (size_t i = 0; i < count; i++) {
    text_add(out, " ", 1);
  }
}

/*
 * Node's reduceToSingleString with compact 3: output (count entries, which grouping may
 * have made from more) on one line between the braces if they fit, else one per line.
 * base is "<ref *n>" or empty. It frees the entries.
 */
static void reduce_to_single_string(inspector *in, text *out, text *output, size_t count, size_t entries, const text *base,
                                    const char *open, const char *close, size_t recurse_times) {
  bool single_line = false;
  if (in->current_depth - recurse_times < 3 && entries == count) {
    size_t start = count + in->indentation + utf16_length(open, strlen(open)) + utf16_length(base->bytes, base->len) + 10;
    single_line = below_break_length(in, output, count, start) && (base->len == 0 || memchr(base->bytes, '\n', base->len) == NULL);
    for (size_t i = 0; single_line && i < count; i++) {
      single_line = memchr(output[i].bytes, '\n', output[i].len) == NULL;
    }
  }
  if (base->len) {
    text_add(out, base->bytes, base->len);
    text_add(out, " ", 1);
  }
  text_puts(out, open);
  for (size_t i = 0; i < count; i++) {
    if (single_line) {
      text_puts(out, i == 0 ? " " : ", ");
    } else {
      text_puts(out, i == 0 ? "\n" : ",\n");
      spaces(out, in->indentation + 2);
    }
    text_add(out, output[i].bytes, output[i].len);
    free(output[i].bytes);
  }
  if (single_line) {
    text_puts(out, " ");
  } else {
    text_add(out, "\n", 1);
    spaces(out, in->indentation);
  }
  text_puts(out, close);
}

/*
 * The opening brace of an object: `Name {`, or `{` for a plain object, which Node
 * recognises by its constructor's name being "Object" (object literals' descriptors use it).
 */
static const char *opening_brace(const pts_type *type, char *buffer, size_t size) {
  if (strcmp(type->name, "Object") == 0) {
    return "{";
  }
  snprintf(buffer, size, "%s {", type->name);
  return buffer;
}

static text *new_entries(size_t count) {
  text *entries = calloc(count ? count : 1, sizeof *entries);
  if (entries == NULL) {
    pts_panic("RangeError", "Out of memory formatting a value for console.log");
  }
  return entries;
}

/*
 * Node's formatMap and formatSet: `key => value` entries of a map, or a set's values, in
 * insertion order, at most max_array_length and then `... n more items`. count is the size
 * on entry and the number of entries on return.
 */
static text *format_map_entries(inspector *in, pts_map *map, size_t *count, size_t recurse_times) {
  size_t shown = *count < in->max_array_length ? *count : in->max_array_length;
  text *entries = new_entries(shown + 1);
  bool is_set = map->type->kind == PTS_KIND_SET;
  in->indentation += 2;
  pts_map_iterator iterator = pts_map_iterate(map);
  size_t i = 0;
  for (; i < shown && pts_map_next(&iterator); i++) {
    format_slot(in, &entries[i], map->type->key, &iterator.key, recurse_times);
    if (!is_set) {
      text_puts(&entries[i], " => ");
      format_slot(in, &entries[i], map->type->element, &iterator.value, recurse_times);
    }
  }
  in->indentation -= 2;
  if (*count > i) {
    size_t remaining = *count - i;
    text_printf(&entries[i++], "... %zu more item%s", remaining, remaining > 1 ? "s" : "");
  }
  *count = i;
  return entries;
}

/*
 * Node's formatRaw for a plain object's properties, here a hash-map object's entries
 * (D-059): `key: value`, in JS's key order (pts_record_iterate), all of them. count is
 * the size on entry and the number of entries on return.
 */
static text *format_record_entries(inspector *in, pts_map *record, size_t *count, size_t recurse_times) {
  text *entries = new_entries(*count);
  in->indentation += 2;
  pts_map_iterator iterator = pts_record_iterate(record);
  size_t i = 0;
  for (; i < *count && pts_map_next(&iterator); i++) {
    format_string_key(&entries[i], (const pts_string *)(uintptr_t)iterator.key);
    text_puts(&entries[i], ": ");
    format_slot(in, &entries[i], record->type->element, &iterator.value, recurse_times);
  }
  in->indentation -= 2;
  *count = i;
  return entries;
}

/* Whether object has a field's property: a field of an optional property may hold the absent value instead (D-065). */
static bool has_field(const pts_object *object, const pts_field *field) {
  return field->kind != PTS_FIELD_VALUE || *(const pts_value *)((const char *)object + field->offset) != PTS_VALUE_ABSENT;
}

/* present_field_count is the number of properties an object's fields hold (has_field). */
static size_t present_field_count(const pts_object *object) {
  size_t count = 0;
  for (uint32_t i = 0; i < object->type->field_count; i++) {
    count += has_field(object, &object->type->fields[i]);
  }
  return count;
}

/* is_numeric_tuple is is_numeric_array for a tuple's elements. */
static bool is_numeric_tuple(const pts_object *tuple, size_t count) {
  const pts_type *type = tuple->type;
  if (count > type->field_count) {
    return false; /* the entry past the elements is `[length]: n` */
  }
  for (uint32_t i = 0; i < type->field_count; i++) {
    const void *slot = (const char *)tuple + type->fields[i].offset;
    switch (type->fields[i].kind) {
    case PTS_FIELD_BOOL:
    case PTS_FIELD_STRING:
    case PTS_FIELD_OBJECT:
      return false;
    case PTS_FIELD_VALUE:
      if (!pts_value_is_number(*(const pts_value *)slot)) {
        return false;
      }
      break;
    default:
      break;
    }
  }
  return true;
}

/* format_tuple_elements formats a tuple's elements as format_elements formats an array's; count is set to the entries made. */
static text *format_tuple_elements(inspector *in, const pts_object *tuple, size_t *count, size_t recurse_times) {
  const pts_type *type = tuple->type;
  size_t length = pts_tuple_length(tuple);
  size_t shown = length < in->max_array_length ? length : in->max_array_length;
  text *entries = new_entries(shown + 1);
  for (uint32_t i = 0; i < shown; i++) {
    in->indentation += 2;
    format_slot(in, &entries[i], type->fields[i].kind, (const char *)tuple + type->fields[i].offset, recurse_times);
    in->indentation -= 2;
  }
  *count = shown;
  if (length > shown) {
    size_t remaining = length - shown;
    text_printf(&entries[(*count)++], "... %zu more item%s", remaining, remaining > 1 ? "s" : "");
  }
  return entries;
}

/* Node's formatRaw for an object's fields: `key: value` entries, in descriptor order, those of well-known symbols (D-111) last. */
static text *format_fields(inspector *in, const pts_object *object, size_t *count, size_t recurse_times) {
  const pts_type *type = object->type;
  text *entries = new_entries(type->field_count);
  size_t n = 0;
  for (int symbols = 0; symbols < 2; symbols++) {
    for (uint32_t i = 0; i < type->field_count; i++) {
      const char *name = type->fields[i].name;
      if (!has_field(object, &type->fields[i]) || pts_name_is_symbol(name) != (symbols == 1)) {
        continue;
      }
      if (symbols == 1) {
        text_printf(&entries[n], "Symbol(Symbol.%s)", name + 2);
      } else {
        format_key(&entries[n], name);
      }
      text_puts(&entries[n], ": ");
      in->indentation += 2;
      format_slot(in, &entries[n], type->fields[i].kind, (const char *)object + type->fields[i].offset, recurse_times);
      in->indentation -= 2;
      n++;
    }
  }
  *count = n;
  return entries;
}

/* Whether an array slot of the given kind is a hole: a zero pointer or dynamic value (D-038, D-085). */
static bool is_hole_slot(pts_field_kind kind, const void *slot) {
  return (kind == PTS_FIELD_STRING || kind == PTS_FIELD_VALUE || kind == PTS_FIELD_OBJECT) && *(const uint64_t *)slot == 0;
}

/*
 * Node's formatArray and formatSpecialArray: an entry per element, a run of holes as one
 * "<n empty items>" entry, at most max_array_length entries, then "... n more items".
 */
static text *format_elements(inspector *in, const pts_array *array, size_t length, size_t *count, size_t recurse_times) {
  size_t max = in->max_array_length;
  text *entries = new_entries(max + 1);
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind), n = 0, index = 0; /* entries made; the index they reach */
  bool undefined_holes = pts_has_undefined_holes(array); /* groups that took no part are undefined in JS */
  for (size_t i = 0; i < length && n < max; i++) {
    const void *slot = pts_array_slot_or_null(array, (double)i, size);
    if (slot != NULL && is_hole_slot(kind, slot) && !undefined_holes) {
      continue;
    }
    if (i != index) {
      text_printf(&entries[n++], "<%zu empty item%s>", i - index, i - index > 1 ? "s" : "");
      index = i;
      if (n == max) {
        break;
      }
    }
    in->indentation += 2;
    if (slot == NULL) {
      text_puts(&entries[n], "undefined"); /* another thread shrank the array meanwhile */
    } else {
      format_slot(in, &entries[n], kind, slot, recurse_times);
    }
    in->indentation -= 2;
    n++, index++;
  }
  size_t remaining = length - index;
  if (n != max && remaining > 0) {
    text_printf(&entries[n++], "<%zu empty item%s>", remaining, remaining > 1 ? "s" : "");
  } else if (remaining > 0) {
    text_printf(&entries[n++], "... %zu more item%s", remaining, remaining > 1 ? "s" : "");
  }
  *count = n;
  return entries;
}

/* The columns code point c takes in a terminal: 1, or what width_ranges says. */
static size_t code_point_width(uint32_t c) {
  size_t low = 0, high = sizeof width_ranges / sizeof width_ranges[0];
  while (low < high) {
    size_t middle = (low + high) / 2;
    if (c < width_ranges[middle].first) {
      high = middle;
    } else if (c > width_ranges[middle].last) {
      low = middle + 1;
    } else {
      return width_ranges[middle].width;
    }
  }
  return 1;
}

/*
 * Node's getStringWidth: the columns a string takes in a terminal, wide East Asian
 * characters and emoji counting two and controls and combining marks none. Node sums
 * the widths of the string's NFC form; this sums its code points' own.
 */
size_t pts_text_width(const char *bytes, size_t len) {
  size_t width = 0;
  for (size_t i = 0; i < len;) {
    unsigned char byte = (unsigned char)bytes[i];
    if (byte < 0x80) {
      width += byte >= 0x20 && byte != 0x7F;
      i++;
      continue;
    }
    size_t size;
    width += code_point_width(decode((const unsigned char *)bytes + i, &size));
    i += size;
  }
  return width;
}

static size_t string_width(const text *entry) { return pts_text_width(entry->bytes, entry->len); }

/*
 * is_numeric_array reports whether the first count elements, which Node groups, are all
 * numbers, so that columns align right; an entry past the end (`[length]: n`) isn't.
 */
static bool is_numeric_array(const pts_array *array, size_t count) {
  if (count > pts_array_length_of(array)) {
    return false;
  }
  switch (array->type->element) {
  case PTS_FIELD_BOOL:
  case PTS_FIELD_STRING:
  case PTS_FIELD_OBJECT:
    return false;
  case PTS_FIELD_VALUE:
    for (size_t i = 0; i < count; i++) {
      const void *slot = pts_array_slot_or_null(array, (double)i, sizeof(pts_value));
      if (slot == NULL || !pts_value_is_number(*(const pts_value *)slot)) {
        return false;
      }
    }
    return true;
  default:
    return true;
  }
}

/* pad writes entry, padded with spaces to width columns: on the left if right_aligned, else on the right. */
static void pad(text *out, const text *entry, const char *suffix, size_t width, bool right_aligned) {
  size_t length = string_width(entry) + strlen(suffix);
  if (right_aligned) {
    spaces(out, width > length ? width - length : 0);
  }
  text_add(out, entry->bytes, entry->len);
  text_puts(out, suffix);
  if (!right_aligned) {
    spaces(out, width > length ? width - length : 0);
  }
}

/*
 * Node's groupArrayElements: more than six short entries are laid out in columns,
 * several to a line. Returns the new entries (and their count), or output unchanged.
 */
static text *group_array_elements(inspector *in, text *output, size_t *count, bool numeric) {
  enum { SEPARATOR_SPACE = 2 };
  size_t output_length = *count;
  if (in->max_array_length < *count) {
    output_length--; /* leave "... n more items" out */
  }
  size_t *data_len = calloc(output_length, sizeof *data_len);
  if (data_len == NULL) {
    pts_panic("RangeError", "Out of memory formatting a value for console.log");
  }
  size_t total_length = 0, max_length = 0;
  for (size_t i = 0; i < output_length; i++) {
    data_len[i] = string_width(&output[i]);
    total_length += data_len[i] + SEPARATOR_SPACE;
    if (max_length < data_len[i]) {
      max_length = data_len[i];
    }
  }
  size_t actual_max = max_length + SEPARATOR_SPACE;
  text *grouped = NULL;
  if ((double)(actual_max * 3 + in->indentation) < in->break_length && ((double)total_length / (double)actual_max > 5 || max_length <= 6)) {
    double average_bias = sqrt((double)actual_max - (double)total_length / (double)*count);
    double biased_max = fmax((double)actual_max - 3 - average_bias, 1);
    double columns = fmin(fmin(round(sqrt(2.5 * biased_max * (double)output_length) / biased_max), floor((in->break_length - (double)in->indentation) / (double)actual_max)),
                          fmin(3 * 4, 15));
    if (columns > 1) {
      size_t cols = (size_t)columns;
      size_t *max_line_length = calloc(cols, sizeof *max_line_length);
      if (max_line_length == NULL) {
        pts_panic("RangeError", "Out of memory formatting a value for console.log");
      }
      for (size_t i = 0; i < cols; i++) {
        size_t line_max_length = 0;
        for (size_t j = i; j < output_length; j += cols) {
          if (data_len[j] > line_max_length) {
            line_max_length = data_len[j];
          }
        }
        max_line_length[i] = line_max_length + SEPARATOR_SPACE;
      }
      bool right_aligned = numeric;
      size_t lines = (output_length + cols - 1) / cols;
      grouped = new_entries(lines + (output_length < *count));
      size_t n = 0;
      for (size_t i = 0; i < output_length; i += cols, n++) {
        size_t max = i + cols < output_length ? i + cols : output_length;
        size_t j = i;
        for (; j < max - 1; j++) {
          pad(&grouped[n], &output[j], ", ", max_line_length[j - i], right_aligned);
        }
        if (right_aligned) {
          pad(&grouped[n], &output[j], "", max_line_length[j - i] - SEPARATOR_SPACE, true);
        } else {
          text_add(&grouped[n], output[j].bytes, output[j].len);
        }
      }
      if (output_length < *count) {
        grouped[n++] = output[output_length];
        output[output_length] = (text){0};
      }
      for (size_t i = 0; i < *count; i++) {
        free(output[i].bytes);
      }
      free(output);
      free(max_line_length);
      *count = n;
    }
  }
  free(data_len);
  return grouped != NULL ? grouped : output;
}

/* append adds entry after the count entries of output, which it may move. */
static text *append(text *output, size_t *count, text entry) {
  output = realloc(output, (*count + 1) * sizeof *output);
  if (output == NULL) {
    pts_panic("RangeError", "Out of memory formatting a value for console.log");
  }
  output[(*count)++] = entry;
  return output;
}

/* hidden_entry is a non-enumerable property's entry, `[key]: ` (Node's formatProperty), with the value still to write. */
static text hidden_entry(const char *key) {
  text entry = {0};
  text_puts(&entry, "[");
  format_key(&entry, key);
  text_puts(&entry, "]: ");
  return entry;
}

/* A match array's own properties after its elements (D-133): `index: 3`, `input: '…'`, `groups: …`. */
static text *format_match_properties(inspector *in, const pts_match_array *match, text *output, size_t *count, size_t recurse_times) {
  const char *keys[] = {"index", "input", "groups"};
  pts_value values[] = {pts_value_from_number(match->index), pts_value_from_string(match->input), match->groups};
  for (size_t i = 0; i < sizeof keys / sizeof keys[0]; i++) {
    text entry = {0};
    text_printf(&entry, "%s: ", keys[i]);
    in->indentation += 2;
    format_value(in, &entry, values[i], recurse_times);
    in->indentation -= 2;
    output = append(output, count, entry);
  }
  return output;
}

/* Whether the method at index is the first accessor of its name in type's method table. */
static bool is_first_accessor(const pts_type *type, uint32_t index) {
  const pts_method *method = &type->methods[index];
  if (method->kind == PTS_METHOD || method->name[0] == '#') {
    return false;
  }
  for (uint32_t i = 0; i < index; i++) {
    if (type->methods[i].kind != PTS_METHOD && strcmp(type->methods[i].name, method->name) == 0) {
      return false;
    }
  }
  return true;
}

/*
 * Node's addPrototypeProperties, which showHidden turns on: the getters and setters that a
 * class instance's classes define, `[name]: [Getter]`, after its own properties. The
 * method table lists a class's accessors before its superclasses'; a getter and a setter
 * of one name show as one even where different classes define them (Node shows only
 * the nearer class's). An error's stack is shown as its own property instead.
 */
static text *format_prototype_accessors(const pts_object *object, text *output, size_t *count) {
  const pts_type *type = object->type;
  for (uint32_t i = 0; i < type->method_count; i++) {
    const char *name = type->methods[i].name;
    if (!is_first_accessor(type, i) || (pts_is_error(object) && strcmp(name, "stack") == 0)) {
      continue;
    }
    bool getter = false, setter = false;
    for (uint32_t j = i; j < type->method_count; j++) {
      if (strcmp(type->methods[j].name, name) == 0) {
        getter = getter || type->methods[j].kind == PTS_GETTER;
        setter = setter || type->methods[j].kind == PTS_SETTER;
      }
    }
    text entry = hidden_entry(name);
    text_puts(&entry, getter && setter ? "[Getter/Setter]" : getter ? "[Getter]" : "[Setter]");
    output = append(output, count, entry);
  }
  return output;
}

/* Whether showHidden shows a class instance's accessors (Node's protoProps): not a plain object's, nor beyond the depth. */
static bool shows_prototype_accessors(const inspector *in, const pts_object *object, size_t recurse_times) {
  return in->show_hidden && !beyond_depth(in, recurse_times) && object->type->kind == PTS_KIND_OBJECT &&
         strcmp(object->type->name, "Object") != 0;
}

/* The number of entries format_prototype_accessors makes. */
static size_t prototype_accessor_count(const pts_object *object) {
  size_t count = 0;
  for (uint32_t i = 0; i < object->type->method_count; i++) {
    count += is_first_accessor(object->type, i) && !(pts_is_error(object) && strcmp(object->type->methods[i].name, "stack") == 0);
  }
  return count;
}

/* circular_reference writes [Circular *n] and returns true if object is already being formatted. */
static bool circular_reference(inspector *in, text *out, const pts_object *object) {
  for (size_t i = 0; i < in->seen_len; i++) {
    if (in->seen[i] == object) {
      size_t index = circular_index(in, object);
      if (index == 0) {
        push(&in->circular, &in->circular_len, &in->circular_cap, object);
        index = in->circular_len;
      }
      text_printf(out, "[Circular *%zu]", index);
      return true;
    }
  }
  return false;
}

/* ---------------------------------------------------------------- errors */

static bool text_contains(const text *haystack, const char *needle, size_t len) {
  if (len == 0) {
    return true;
  }
  for (size_t i = 0; i + len <= haystack->len; i++) {
    if (memcmp(haystack->bytes + i, needle, len) == 0) {
      return true;
    }
  }
  return false;
}

/* The index of the first needle in haystack at or after from, or -1. */
static ptrdiff_t text_index(const text *haystack, const char *needle, size_t len, size_t from) {
  for (size_t i = from; i + len <= haystack->len; i++) {
    if (memcmp(haystack->bytes + i, needle, len) == 0) {
      return (ptrdiff_t)i;
    }
  }
  return -1;
}

/*
 * Node's improveStack: a stack that starts with the error's name, a name ending in
 * "Error", gets the class's name in its place where they differ: `HttpError: …` for a
 * class HttpError whose name is "Error", `MyErr [Error]: …` for a class MyErr.
 */
static void improve_stack(text *stack, const char *class_name, const pts_string *name) {
  size_t len = name->byte_len;
  bool ends_with_error = len >= 5 && memcmp(name->bytes + len - 5, "Error", 5) == 0;
  bool starts_with_name = stack->len >= len && memcmp(stack->bytes, name->bytes, len) == 0 &&
                          (stack->len == len || stack->bytes[len] == ':' || stack->bytes[len] == '\n');
  if (!ends_with_error || !starts_with_name || (strlen(class_name) == len && memcmp(class_name, name->bytes, len) == 0)) {
    return;
  }
  text improved = {0};
  text_puts(&improved, class_name);
  text prefix = {.bytes = (char *)class_name, .len = strlen(class_name)};
  if (!text_contains(&prefix, name->bytes, len)) {
    text_printf(&improved, " [%.*s]", (int)len, name->bytes);
  }
  text_add(&improved, stack->bytes + len, stack->len - len);
  free(stack->bytes);
  *stack = improved;
}

/*
 * Node's formatError: the error's stack (its name and message until stacks have frames,
 * M3.2b), improved, in brackets if it has no frames, indented as the error is; then, as
 * an object's, the properties of the classes derived from Error, its name if the stack
 * doesn't show it, and `[cause]`. message is not enumerable in JS.
 */
static void format_error(inspector *in, text *out, pts_error *error, size_t recurse_times) {
  pts_value stack_value = pts_error_get_stack(error);
  pts_string *stack_string = pts_value_truthy(stack_value) ? pts_value_to_string(stack_value) : pts_error_to_string(error);
  text stack = {0};
  text_add(&stack, stack_string->bytes, stack_string->byte_len);
  /* Each field read once: another thread may set them while this runs, and the entries are counted before they are made (D-143). */
  pts_string *name = error->name, *message = error->message;
  pts_value cause = __atomic_load_n(&error->cause, __ATOMIC_RELAXED);
  bool shows_name = !text_contains(&stack, name->bytes, name->byte_len);
  const pts_type *type = error->pts_header;
  size_t keys = shows_name + (cause != PTS_VALUE_ABSENT);
  for (uint32_t i = 0; i < type->field_count; i++) {
    keys += type->fields[i].offset >= sizeof(pts_error) && has_field((const pts_object *)error, &type->fields[i]);
  }
  bool accessors = shows_prototype_accessors(in, (const pts_object *)error, recurse_times);
  if (in->show_hidden) {
    keys += 1 + (message->byte_len > 0) + (accessors ? prototype_accessor_count((const pts_object *)error) : 0);
  }

  improve_stack(&stack, type->name, name);
  ptrdiff_t position = -1;
  if (message->byte_len > 0) {
    position = text_index(&stack, message->bytes, message->byte_len, 0);
    if (position > 0) {
      position += message->byte_len;
    } else {
      position = -1; /* Node's `indexOf(…) || -1` also discards a message at 0 */
    }
  }
  text base = {0};
  size_t reference = circular_index(in, (const pts_object *)error);
  if (reference != 0) {
    text_printf(&base, "<ref *%zu> ", reference);
  }
  if (text_index(&stack, "\n    at", 7, position < 0 ? 0 : (size_t)position) < 0) {
    text_puts(&base, "[");
    text_add(&base, stack.bytes, stack.len);
    text_puts(&base, "]");
  } else {
    for (size_t i = 0; i < stack.len; i++) {
      text_add(&base, &stack.bytes[i], 1);
      if (stack.bytes[i] == '\n') {
        spaces(&base, in->indentation);
      }
    }
  }
  free(stack.bytes);
  if (keys == 0) {
    text_add(out, base.bytes, base.len);
    free(base.bytes);
    return;
  }
  if (beyond_depth(in, recurse_times)) {
    text_printf(out, "[%s]", type->name);
    free(base.bytes);
    return;
  }

  recurse_times++;
  push(&in->seen, &in->seen_len, &in->seen_cap, (const pts_object *)error);
  in->current_depth = recurse_times;
  text *entries = new_entries(4 + type->field_count); /* room for every field, which another thread may set after they were counted */
  size_t count = 0;
  if (in->show_hidden) { /* own properties that aren't enumerable, in the order V8 makes them */
    entries[count++] = hidden_entry("stack");
    text_puts(&entries[count - 1], "[Getter/Setter]");
    if (message->byte_len > 0) {
      entries[count] = hidden_entry("message");
      quote_string(&entries[count++], message);
    }
    if (cause != PTS_VALUE_ABSENT) {
      entries[count] = hidden_entry("cause");
      in->indentation += 2;
      format_value(in, &entries[count++], cause, recurse_times);
      in->indentation -= 2;
    }
  }
  if (shows_name) {
    text_puts(&entries[count], "name: ");
    quote_string(&entries[count++], name);
  }
  for (uint32_t i = 0; i < type->field_count; i++) {
    const pts_field *field = &type->fields[i];
    if (field->offset < sizeof(pts_error) || !has_field((const pts_object *)error, field)) {
      continue; /* name, message and cause, which Error itself holds */
    }
    format_key(&entries[count], field->name);
    text_puts(&entries[count], ": ");
    in->indentation += 2;
    format_slot(in, &entries[count++], field->kind, (const char *)error + field->offset, recurse_times);
    in->indentation -= 2;
  }
  if (cause != PTS_VALUE_ABSENT && !in->show_hidden) {
    text_puts(&entries[count], "[cause]: ");
    in->indentation += 2;
    format_value(in, &entries[count++], cause, recurse_times);
    in->indentation -= 2;
  }
  if (accessors) {
    entries = format_prototype_accessors((const pts_object *)error, entries, &count);
  }
  in->seen_len--;
  reduce_to_single_string(in, out, entries, count, count, &base, "{", "}", recurse_times);
  free(entries);
  free(base.bytes);
}

/*
 * Node's formatValue and formatRaw for an object or array: `Name { key: value, … }`,
 * `{ key: value, … }` for a plain object, `[ element, … ]` for an array; `[Name]` or
 * `[Array]` beyond depth 2 unless empty; `<ref *n>` and `[Circular *n]` for cycles.
 */
static void format_heap_object(inspector *in, text *out, const pts_object *object, size_t recurse_times) {
  if (circular_reference(in, out, object)) {
    return;
  }
  if (pts_is_error(object)) {
    format_error(in, out, (pts_error *)object, recurse_times);
    return;
  }
  const pts_type *type = object->type;
  bool is_tuple = type->kind == PTS_KIND_TUPLE;
  bool is_array = type->kind == PTS_KIND_ARRAY || is_tuple;
  bool is_map = type->kind == PTS_KIND_MAP || type->kind == PTS_KIND_SET;
  bool is_record = type->kind == PTS_KIND_RECORD;
  size_t length = is_tuple ? pts_tuple_length(object) : is_array ? pts_array_length_of((const pts_array *)object) : type->field_count;
  if (is_record) {
    length = (size_t)pts_map_size((const pts_map *)object);
  } else if (!is_array && !is_map) {
    length = present_field_count(object);
  }
  char buffer[256];
  const char *open = is_array ? "[" : opening_brace(type, buffer, sizeof buffer);
  const char *close = is_array ? "]" : "}";
  if (is_map) {
    length = (size_t)pts_map_size((const pts_map *)object);
    snprintf(buffer, sizeof buffer, "%s(%zu) {", type->name, length); /* Node's `Map(2) {` */
    open = buffer;
  }
  bool hidden_length = is_array && in->show_hidden; /* Node's `[length]: n` */
  bool accessors = shows_prototype_accessors(in, object, recurse_times) && prototype_accessor_count(object) > 0;
  if (length == 0 && !hidden_length && !accessors) {
    text_printf(out, "%s%s", open, close);
    return;
  }
  if (beyond_depth(in, recurse_times)) {
    text_printf(out, "[%s]", type->name);
    return;
  }
  recurse_times++;
  push(&in->seen, &in->seen_len, &in->seen_cap, object);
  in->current_depth = recurse_times;
  size_t count = length;
  text *output = is_tuple ? format_tuple_elements(in, object, &count, recurse_times)
                 : is_array ? format_elements(in, (const pts_array *)object, length, &count, recurse_times)
                 : is_map    ? format_map_entries(in, (pts_map *)object, &count, recurse_times)
                 : is_record ? format_record_entries(in, (pts_map *)object, &count, recurse_times)
                             : format_fields(in, object, &count, recurse_times);
  if (hidden_length) {
    text entry = hidden_entry("length");
    text_printf(&entry, "%zu", length);
    output = append(output, &count, entry);
  }
  if (accessors) {
    output = format_prototype_accessors(object, output, &count);
  }
  if (is_array && pts_is_match_array(object)) {
    output = format_match_properties(in, (const pts_match_array *)object, output, &count, recurse_times);
  }
  in->seen_len--;

  text base = {0};
  size_t reference = circular_index(in, object);
  if (reference != 0) {
    text_printf(&base, "<ref *%zu>", reference);
  }
  size_t entries = count;
  if (is_array && entries > 6) {
    bool numeric = is_tuple ? is_numeric_tuple(object, count) : is_numeric_array((const pts_array *)object, count);
    output = group_array_elements(in, output, &count, numeric);
  }
  reduce_to_single_string(in, out, output, count, entries, &base, open, close, recurse_times);
  free(output);
  free(base.bytes);
}

/*
 * Node's formatTypedArray: `Uint8Array(3) [ 1, 2, 3 ]`, at most max_array_length elements,
 * grouped in columns as a number array's; showHidden adds the view's properties (D-125).
 */
static void format_typed_array(inspector *in, text *out, const pts_typed_array *array, size_t recurse_times) {
  const char *name = array->type->name;
  if (array->length == 0 && !in->show_hidden) {
    text_printf(out, "%s(0) []", name);
    return;
  }
  if (beyond_depth(in, recurse_times)) {
    text_printf(out, "[%s]", name);
    return;
  }
  recurse_times++;
  in->current_depth = recurse_times;
  size_t shown = array->length < in->max_array_length ? array->length : in->max_array_length;
  text *output = new_entries(shown + 1);
  size_t count = 0;
  for (; count < shown; count++) {
    format_number(&output[count], pts_typed_array_as_number(array, count));
  }
  if (array->length > shown) {
    size_t remaining = array->length - shown;
    text_printf(&output[count++], "... %zu more item%s", remaining, remaining > 1 ? "s" : "");
  }
  if (in->show_hidden) {
    const char *keys[] = {"BYTES_PER_ELEMENT", "length", "byteLength", "byteOffset"};
    double values[] = {pts_typed_array_bytes_per_element(array), pts_typed_array_length(array), pts_typed_array_byte_length(array), pts_typed_array_byte_offset(array)};
    for (size_t i = 0; i < 4; i++) {
      text entry = hidden_entry(keys[i]);
      format_number(&entry, values[i]);
      output = append(output, &count, entry);
    }
    text entry = hidden_entry("buffer");
    in->indentation += 2;
    format_array_buffer(in, &entry, array->buffer, recurse_times, false);
    in->indentation -= 2;
    output = append(output, &count, entry);
  }
  size_t entries = count;
  if (entries > 6) {
    output = group_array_elements(in, output, &count, !in->show_hidden);
  }
  char open[64];
  snprintf(open, sizeof open, "%s(%zu) [", name, array->length);
  text base = {0};
  reduce_to_single_string(in, out, output, count, entries, &base, open, "]", recurse_times);
  free(output);
}

/*
 * Node's formatArrayBuffer: `ArrayBuffer { [Uint8Contents]: <00 01>, [byteLength]: 2 }`,
 * the contents left out for a typed array's [buffer] (D-125).
 */
static void format_array_buffer(inspector *in, text *out, const pts_arraybuffer *buffer, size_t recurse_times, bool contents) {
  if (beyond_depth(in, recurse_times)) {
    text_puts(out, "[ArrayBuffer]");
    return;
  }
  recurse_times++;
  in->current_depth = recurse_times;
  text *output = new_entries(2);
  size_t count = 0;
  if (contents) {
    size_t shown = buffer->byte_length < in->max_array_length ? buffer->byte_length : in->max_array_length;
    text_puts(&output[count], "[Uint8Contents]: <");
    for (size_t i = 0; i < shown; i++) {
      text_printf(&output[count], i > 0 ? " %02x" : "%02x", buffer->bytes[i]);
    }
    if (buffer->byte_length > shown) {
      size_t remaining = buffer->byte_length - shown;
      text_printf(&output[count], " ... %zu more byte%s", remaining, remaining > 1 ? "s" : "");
    }
    text_puts(&output[count++], ">");
  }
  output[count] = hidden_entry("byteLength");
  format_number(&output[count++], pts_arraybuffer_byte_length(buffer));
  text base = {0};
  reduce_to_single_string(in, out, output, count, count, &base, "ArrayBuffer {", "}", recurse_times);
  free(output);
}

/* Node's formatPromise: `Promise { <pending> }`, `Promise { value }` or `Promise { <rejected> reason }` (D-112). */
static void format_promise(inspector *in, text *out, const pts_object *promise, size_t recurse_times) {
  if (beyond_depth(in, recurse_times)) {
    text_puts(out, "[Promise]");
    return;
  }
  recurse_times++;
  in->current_depth = recurse_times;
  text *output = new_entries(1);
  pts_value result;
  switch (pts_promise_state(promise, &result)) {
  case 0:
    text_puts(&output[0], "<pending>");
    break;
  case 2:
    text_puts(&output[0], "<rejected> ");
    /* fall through */
  default:
    in->indentation += 2;
    format_value(in, &output[0], result, recurse_times);
    in->indentation -= 2;
  }
  text base = {0};
  reduce_to_single_string(in, out, output, 1, 1, &base, "Promise {", "}", recurse_times);
  free(output);
}

/* Node's inspect(value, { depth, showHidden }), appended to out: top-level strings are quoted too. */
static void inspect_value(text *out, pts_value value, double depth, bool show_hidden) {
  inspector in = new_inspector(depth, show_hidden);
  format_value(&in, out, value, 0);
  free_inspector(&in);
}

/* ---------------------------------------------------------------- format strings */

static bool is_string_value(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_STRING;
}

/* A number as formatNumber writes it ("-0" for negative zero); a boxed 64-bit integer with every digit. */
static void format_numeric(text *out, pts_value value) {
  if (pts_value_is_number(value)) {
    format_number(out, pts_value_as_number(value));
    return;
  }
  const pts_object *object = pts_value_as_object(value);
  if (object->type->kind == PTS_KIND_INT64) {
    text_printf(out, "%" PRId64, ((const pts_boxed_int64 *)object)->value);
  } else {
    text_printf(out, "%" PRIu64, ((const pts_boxed_uint64 *)object)->value);
  }
}

static bool is_numeric_value(pts_value value) {
  if (pts_value_is_number(value)) {
    return true;
  }
  pts_kind kind = pts_value_is_object(value) ? pts_value_as_object(value)->type->kind : 0;
  return kind == PTS_KIND_INT64 || kind == PTS_KIND_UINT64;
}

/*
 * Whether String(object) calls the program's own toString: Node's hasBuiltInToString,
 * negated. Arrays, maps, sets, dates, hash-map objects and instances without a toString
 * method have JS's own. An error's is taken to be Error's even where its class overrides it.
 */
static bool has_program_to_string(const pts_object *object) {
  if (object->type->kind != PTS_KIND_OBJECT || pts_is_error(object)) {
    return false;
  }
  for (uint32_t i = 0; i < object->type->method_count; i++) {
    if (object->type->methods[i].kind == PTS_METHOD && strcmp(object->type->methods[i].name, "toString") == 0) {
      return true;
    }
  }
  return false;
}

/* %s: String(value), except that numbers keep "-0" and objects with JS's own toString are inspected one level deep. */
static void format_as_string(text *out, pts_value value) {
  if (is_numeric_value(value)) {
    format_numeric(out, value);
    return;
  }
  if (pts_value_is_object(value) && pts_value_as_object(value)->type->kind != PTS_KIND_STRING &&
      pts_value_as_object(value)->type->kind != PTS_KIND_FUNCTION && !has_program_to_string(pts_value_as_object(value))) {
    inspect_value(out, value, 0, false);
    return;
  }
  pts_string *string = pts_value_to_string(value);
  text_add(out, string->bytes, string->byte_len);
}

/* Whether exception is the TypeError JSON.stringify throws for a circular structure. */
static bool is_circular_json_error(pts_value exception) {
  static const char message[] = "Converting circular structure to JSON";
  if (!pts_value_is_object(exception) || !pts_is_error(pts_value_as_object(exception))) {
    return false;
  }
  const pts_error *error = (const pts_error *)pts_value_as_object(exception);
  const pts_string *name = error->name, *text = error->message; /* once each (D-143) */
  return name->byte_len == 9 && memcmp(name->bytes, "TypeError", 9) == 0 && text->byte_len >= sizeof message - 1 &&
         memcmp(text->bytes, message, sizeof message - 1) == 0;
}

/* %j: Node's tryStringify, JSON.stringify(value) with "[Circular]" for a circular structure; "undefined" where it gives undefined. */
static void format_as_json(text *out, pts_value value) {
  pts_handler handler;
  pts_value json;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    json = pts_json_stringify(value, PTS_VALUE_UNDEFINED, PTS_VALUE_UNDEFINED);
    pts_try_leave(&handler);
  } else {
    pts_value exception = pts_caught(&handler);
    if (!is_circular_json_error(exception)) {
      pts_throw(exception);
    }
    text_puts(out, "[Circular]");
    return;
  }
  pts_string *string = pts_value_to_string(json);
  text_add(out, string->bytes, string->byte_len);
}

/* %i and %f: parseInt(value) and parseFloat(value); a 64-bit integer with every digit. */
static void format_parsed(text *out, pts_value value, bool integer) {
  if (!pts_value_is_number(value) && is_numeric_value(value)) {
    format_numeric(out, value);
    return;
  }
  pts_string *string = pts_value_to_string(value);
  format_number(out, integer ? pts_parse_int(string, 0) : pts_parse_float(string));
}

/*
 * Formats the argument for the placeholder %<letter> (Node's formatWithOptionsInternal)
 * and returns true, or returns false if the letter isn't a placeholder's.
 */
static bool format_placeholder(text *out, char letter, pts_value argument) {
  switch (letter) {
  case 's':
    format_as_string(out, argument);
    return true;
  case 'j':
    format_as_json(out, argument);
    return true;
  case 'd':
    if (is_numeric_value(argument)) {
      format_numeric(out, argument);
    } else {
      format_number(out, pts_value_to_number(argument));
    }
    return true;
  case 'O':
    inspect_value(out, argument, DEPTH, false);
    return true;
  case 'o':
    inspect_value(out, argument, 4, true);
    return true;
  case 'i':
  case 'f':
    format_parsed(out, argument, letter == 'i');
    return true;
  case 'c': /* CSS, which a terminal ignores */
    return true;
  default:
    return false;
  }
}

/*
 * Node's formatWithOptions: a first argument that is a string is a format whose
 * placeholders (%s, %d, %i, %f, %j, %o, %O, %c) take the next arguments and whose %%
 * is %; the arguments left over follow, separated by spaces, strings as they are and
 * other values inspected.
 */
static void format_arguments(text *out, size_t argc, const pts_value *argv) {
  size_t used = 0; /* Node's a: the index of the last argument a placeholder took */
  const char *separator = "";
  if (argc > 0 && is_string_value(argv[0])) {
    const pts_string *format = (const pts_string *)pts_value_as_object(argv[0]);
    if (argc == 1) {
      text_add(out, format->bytes, format->byte_len);
      return;
    }
    const char *bytes = format->bytes;
    size_t last = 0; /* Node's lastPos: where the format's text not yet copied starts */
    for (size_t i = 0; i + 1 < format->byte_len; i++) {
      if (bytes[i] != '%') {
        continue;
      }
      char letter = bytes[++i];
      if (used + 1 != argc) {
        if (letter == '%') {
          text_add(out, bytes + last, i - last);
          last = i + 1;
          continue;
        }
        text piece = {0};
        if (!format_placeholder(&piece, letter, argv[used + 1])) {
          continue;
        }
        used++;
        text_add(out, bytes + last, i - 1 - last);
        text_add(out, piece.bytes, piece.len);
        free(piece.bytes);
        last = i + 1;
      } else if (letter == '%') {
        text_add(out, bytes + last, i - last);
        last = i + 1;
      }
    }
    if (last != 0) {
      used++;
      separator = " ";
      text_add(out, bytes + last, format->byte_len - last);
    }
  }
  for (size_t a = used; a < argc; a++) {
    text_puts(out, separator);
    separator = " ";
    if (is_string_value(argv[a])) {
      const pts_string *string = (const pts_string *)pts_value_as_object(argv[a]);
      text_add(out, string->bytes, string->byte_len);
    } else {
      inspect_value(out, argv[a], DEPTH, false);
    }
  }
}

/* ---------------------------------------------------------------- console.c's entry points */

/* console.log's arguments as Node formats them, in a NUL-terminated buffer on the C heap that the caller frees. */
char *pts_format(size_t argc, const pts_value *argv, size_t *len) {
  text result = {0};
  text_add(&result, "", 0);
  format_arguments(&result, argc, argv);
  *len = result.len;
  return result.bytes;
}

/* util.inspect(value, { depth, showHidden }) (depth INFINITY for null), as pts_format returns it. */
char *pts_inspect(pts_value value, double depth, bool show_hidden, size_t *len) {
  text result = {0};
  text_add(&result, "", 0);
  inspect_value(&result, value, depth, show_hidden);
  *len = result.len;
  return result.bytes;
}

/*
 * console.table's _inspect: a non-array object with more than two keys as [Name], other
 * values one level deep, on one line, with at most three elements of an array, map or set.
 */
void pts_inspect_cell(text *out, pts_value value) {
  bool is_object = pts_value_is_object(value) && pts_value_as_object(value)->type->kind != PTS_KIND_STRING &&
                   pts_value_as_object(value)->type->kind != PTS_KIND_INT64 && pts_value_as_object(value)->type->kind != PTS_KIND_UINT64 &&
                   pts_value_as_object(value)->type->kind != PTS_KIND_FUNCTION;
  bool summarized = is_object && !pts_value_is_array(value) && pts_array_length_of(pts_value_key_array(value)) > 2;
  inspector in = new_inspector(summarized ? -1 : 0, false);
  in.break_length = INFINITY;
  in.max_array_length = 3;
  format_value(&in, out, value, 0);
  free_inspector(&in);
}

/* Writes value as console.log shows it nested in an object (strings quoted): for an uncaught exception. */
void pts_inspect_value_write(FILE *out, pts_value value) {
  text result = {0};
  inspect_value(&result, value, DEPTH, false);
  fwrite(result.bytes, 1, result.len, out);
  free(result.bytes);
}
