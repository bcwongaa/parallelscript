/*
 * console.log of objects: a port of the parts of Node's util.inspect
 * (lib/internal/util/inspect.js) that class instances need, with its defaults
 * (D-O16): depth 2, breakLength 80, compact 3. Instances are read through their
 * type descriptors (D-024). The full port, with arrays, maps and format strings,
 * is M3.6.
 */
#include "pts.h"

#include <inttypes.h>
#include <math.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

enum { DEPTH = 2, BREAK_LENGTH = 80, MAX_ARRAY_LENGTH = 100 };

/* A growable, NUL-terminated byte string on the C heap (inspect output holds no GC pointers). */
typedef struct text {
  char *bytes;
  size_t len, cap;
} text;

static void text_add(text *t, const char *bytes, size_t len) {
  if (t->len + len + 1 > t->cap) {
    t->cap = (t->len + len + 1) * 2;
    t->bytes = realloc(t->bytes, t->cap);
    if (t->bytes == NULL) {
      pts_panic("RangeError", "Out of memory formatting a value for console.log");
    }
  }
  memcpy(t->bytes + t->len, bytes, len);
  t->len += len;
  t->bytes[t->len] = '\0';
}

static void text_puts(text *t, const char *cstr) { text_add(t, cstr, strlen(cstr)); }

static void text_printf(text *t, const char *format, ...) __attribute__((format(printf, 2, 3)));

static void text_printf(text *t, const char *format, ...) {
  char small[64];
  va_list args;
  va_start(args, format);
  int len = vsnprintf(small, sizeof small, format, args);
  va_end(args);
  text_add(t, small, (size_t)len); /* callers format short numbers and references */
}

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
} inspector;

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
static void quote_string(text *out, const pts_string *string) {
  const char *bytes = string->bytes;
  char quote = '\'';
  if (memchr(bytes, '\'', string->byte_len) != NULL) {
    if (memchr(bytes, '"', string->byte_len) == NULL) {
      quote = '"';
    } else if (memchr(bytes, '`', string->byte_len) == NULL && strstr(bytes, "${") == NULL) {
      quote = '`';
    }
  }
  text_add(out, &quote, 1);
  size_t last = 0;
  for (size_t i = 0; i < string->byte_len;) {
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
  text_add(out, bytes + last, string->byte_len - last);
  text_add(out, &quote, 1);
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

/* Node's formatting of a function without properties: `[Function: name]`, or `[Function (anonymous)]`. */
static void format_function(text *out, const pts_closure *closure) {
  while (closure->type->name == NULL) {
    closure = ((const pts_closure *const *)(closure + 1))[0]; /* an adapter shows as the function it adapts, its first capture */
  }
  if (closure->type->name[0] == '\0') {
    text_puts(out, "[Function (anonymous)]");
  } else {
    text_printf(out, "[Function: %s]", closure->type->name);
  }
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
    quote_string(out, (const pts_string *)object);
    return;
  case PTS_KIND_INT64:
    text_printf(out, "%" PRId64, ((const pts_boxed_int64 *)object)->value);
    return;
  case PTS_KIND_UINT64:
    text_printf(out, "%" PRIu64, ((const pts_boxed_uint64 *)object)->value);
    return;
  case PTS_KIND_OBJECT:
  case PTS_KIND_ARRAY:
    format_heap_object(in, out, object, recurse_times);
    return;
  case PTS_KIND_FUNCTION:
    format_function(out, (const pts_closure *)object);
    return;
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
  case PTS_FIELD_STRING:
    if (SLOT(pts_string *) == NULL) {
      text_puts(out, "undefined"); /* a hole that threads racing on an array left */
    } else {
      quote_string(out, SLOT(pts_string *));
    }
    return;
  case PTS_FIELD_VALUE:
    format_value(in, out, SLOT(pts_value) == 0 ? PTS_VALUE_UNDEFINED : SLOT(pts_value), recurse_times);
    return;
  case PTS_FIELD_OBJECT:
    if (SLOT(pts_object *) == NULL) {
      text_puts(out, "undefined"); /* a field read before the constructor assigned it */
    } else {
      format_value(in, out, pts_value_from_object(SLOT(pts_object *)), recurse_times);
    }
    return;
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
  }
#undef SLOT
}

/* A key prints bare if it matches Node's keyStrRegExp, ^[a-zA-Z_][a-zA-Z_0-9]*$, else quoted. */
static void format_key(text *out, const char *name) {
  bool bare = name[0] != '\0' && !(name[0] >= '0' && name[0] <= '9');
  for (const char *c = name; *c && bare; c++) {
    bare = *c == '_' || (*c >= 'a' && *c <= 'z') || (*c >= 'A' && *c <= 'Z') || (*c >= '0' && *c <= '9');
  }
  if (bare) {
    text_puts(out, name);
    return;
  }
  pts_string *key = pts_string_from_cstr(name);
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

/* Node's isBelowBreakLength: whether the entries fit on one line after start columns. */
static bool below_break_length(text *entries, size_t count, size_t start) {
  size_t total = count + start;
  if (total + count > BREAK_LENGTH) {
    return false;
  }
  for (size_t i = 0; i < count; i++) {
    total += utf16_length(entries[i].bytes, entries[i].len);
    if (total > BREAK_LENGTH) {
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
    single_line = below_break_length(output, count, start);
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

/* Node's formatRaw for an object's fields: `key: value` entries, in descriptor order. */
static text *format_fields(inspector *in, const pts_object *object, size_t recurse_times) {
  const pts_type *type = object->type;
  text *entries = new_entries(type->field_count);
  for (uint32_t i = 0; i < type->field_count; i++) {
    format_key(&entries[i], type->fields[i].name);
    text_puts(&entries[i], ": ");
    in->indentation += 2;
    format_slot(in, &entries[i], type->fields[i].kind, (const char *)object + type->fields[i].offset, recurse_times);
    in->indentation -= 2;
  }
  return entries;
}

/* Node's formatArray: an entry per element, at most MAX_ARRAY_LENGTH, then "... n more items". */
static text *format_elements(inspector *in, const pts_array *array, size_t length, size_t *count, size_t recurse_times) {
  size_t shown = length < MAX_ARRAY_LENGTH ? length : MAX_ARRAY_LENGTH;
  *count = shown + (length > shown);
  text *entries = new_entries(*count);
  size_t size = pts_field_kind_size(array->type->element);
  for (size_t i = 0; i < shown; i++) {
    const void *slot = pts_array_slot_or_null(array, (double)i, size);
    in->indentation += 2;
    if (slot == NULL) {
      text_puts(&entries[i], "undefined"); /* another thread shrank the array meanwhile */
    } else {
      format_slot(in, &entries[i], array->type->element, slot, recurse_times);
    }
    in->indentation -= 2;
  }
  if (length > shown) {
    size_t remaining = length - shown;
    text_printf(&entries[shown], "... %zu more item%s", remaining, remaining > 1 ? "s" : "");
  }
  return entries;
}

/* Node's getStringWidth, approximated by counting code points (wide and zero-width characters count as one). */
static size_t string_width(const text *entry) {
  size_t width = 0;
  for (size_t i = 0; i < entry->len; i++) {
    width += ((unsigned char)entry->bytes[i] & 0xC0) != 0x80;
  }
  return width;
}

/* is_numeric_array reports whether every element Node would group is a number, so that columns align right. */
static bool is_numeric_array(const pts_array *array, size_t count) {
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
static text *group_array_elements(inspector *in, text *output, size_t *count, const pts_array *array) {
  enum { SEPARATOR_SPACE = 2 };
  size_t output_length = *count;
  if (MAX_ARRAY_LENGTH < *count) {
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
  if (actual_max * 3 + in->indentation < BREAK_LENGTH && ((double)total_length / (double)actual_max > 5 || max_length <= 6)) {
    double average_bias = sqrt((double)actual_max - (double)total_length / (double)*count);
    double biased_max = fmax((double)actual_max - 3 - average_bias, 1);
    double columns = fmin(fmin(round(sqrt(2.5 * biased_max * (double)output_length) / biased_max), floor((double)(BREAK_LENGTH - in->indentation) / (double)actual_max)),
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
      bool right_aligned = is_numeric_array(array, *count);
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

/*
 * Node's formatValue and formatRaw for an object or array: `Name { key: value, … }`,
 * `{ key: value, … }` for a plain object, `[ element, … ]` for an array; `[Name]` or
 * `[Array]` beyond depth 2 unless empty; `<ref *n>` and `[Circular *n]` for cycles.
 */
static void format_heap_object(inspector *in, text *out, const pts_object *object, size_t recurse_times) {
  if (circular_reference(in, out, object)) {
    return;
  }
  const pts_type *type = object->type;
  bool is_array = type->kind == PTS_KIND_ARRAY;
  size_t length = is_array ? pts_array_length_of((const pts_array *)object) : type->field_count;
  char buffer[256];
  const char *open = is_array ? "[" : opening_brace(type, buffer, sizeof buffer);
  const char *close = is_array ? "]" : "}";
  if (length == 0) {
    text_printf(out, "%s%s", open, close);
    return;
  }
  if (recurse_times > DEPTH) {
    text_printf(out, "[%s]", type->name);
    return;
  }
  recurse_times++;
  push(&in->seen, &in->seen_len, &in->seen_cap, object);
  in->current_depth = recurse_times;
  size_t count = length;
  text *output = is_array ? format_elements(in, (const pts_array *)object, length, &count, recurse_times) : format_fields(in, object, recurse_times);
  in->seen_len--;

  text base = {0};
  size_t reference = circular_index(in, object);
  if (reference != 0) {
    text_printf(&base, "<ref *%zu>", reference);
  }
  size_t entries = count;
  if (is_array && entries > 6) {
    output = group_array_elements(in, output, &count, (const pts_array *)object);
  }
  reduce_to_single_string(in, out, output, count, entries, &base, open, close, recurse_times);
  free(output);
  free(base.bytes);
}

void pts_inspect_write(FILE *out, const pts_object *object) {
  inspector in = {0};
  text result = {0};
  if (object->type->kind == PTS_KIND_FUNCTION) {
    format_function(&result, (const pts_closure *)object);
  } else {
    format_heap_object(&in, &result, object, 0);
  }
  fwrite(result.bytes, 1, result.len, out);
  free(result.bytes);
  free(in.seen);
  free(in.circular);
}
