/*
 * console.log of objects: a port of the parts of Node's util.inspect
 * (lib/internal/util/inspect.js) that class instances need, with its defaults
 * (D-O16): depth 2, breakLength 80, compact 3. Instances are read through their
 * type descriptors (D-024). The full port, with arrays, maps and format strings,
 * is M3.6.
 */
#include "pts.h"

#include <inttypes.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

enum { DEPTH = 2, BREAK_LENGTH = 80 };

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

static void format_object(inspector *in, text *out, const pts_object *object, size_t recurse_times);

/* Node's formatValue for a value nested in an object: strings are quoted. */
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
    format_object(in, out, object, recurse_times);
    return;
  }
}

/* format_field formats the value a field of object holds. */
static void format_field(inspector *in, text *out, const pts_object *object, const pts_field *field, size_t recurse_times) {
  const char *slot = (const char *)object + field->offset;
#define FIELD(type) (*(const type *)(const void *)slot)
  switch (field->kind) {
  case PTS_FIELD_NUMBER:
    format_number(out, FIELD(double));
    return;
  case PTS_FIELD_F32:
    format_number(out, FIELD(float));
    return;
  case PTS_FIELD_BOOL:
    text_puts(out, FIELD(bool) ? "true" : "false");
    return;
  case PTS_FIELD_STRING:
    quote_string(out, FIELD(const pts_string *));
    return;
  case PTS_FIELD_VALUE:
    format_value(in, out, FIELD(pts_value), recurse_times);
    return;
  case PTS_FIELD_OBJECT:
    if (FIELD(const pts_object *) == NULL) {
      text_puts(out, "undefined"); /* read before the constructor assigned it */
    } else {
      format_value(in, out, pts_value_from_object(FIELD(const pts_object *)), recurse_times);
    }
    return;
  case PTS_FIELD_I8:
    text_printf(out, "%d", FIELD(int8_t));
    return;
  case PTS_FIELD_I16:
    text_printf(out, "%d", FIELD(int16_t));
    return;
  case PTS_FIELD_I32:
    text_printf(out, "%" PRId32, FIELD(int32_t));
    return;
  case PTS_FIELD_I64:
    text_printf(out, "%" PRId64, FIELD(int64_t));
    return;
  case PTS_FIELD_U8:
    text_printf(out, "%u", FIELD(uint8_t));
    return;
  case PTS_FIELD_U16:
    text_printf(out, "%u", FIELD(uint16_t));
    return;
  case PTS_FIELD_U32:
    text_printf(out, "%" PRIu32, FIELD(uint32_t));
    return;
  case PTS_FIELD_U64:
    text_printf(out, "%" PRIu64, FIELD(uint64_t));
    return;
  }
#undef FIELD
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

/* Node's formatRaw and reduceToSingleString for a class instance: `Name { key: value, … }`. */
static void format_object(inspector *in, text *out, const pts_object *object, size_t recurse_times) {
  const pts_type *type = object->type;
  for (size_t i = 0; i < in->seen_len; i++) {
    if (in->seen[i] == object) {
      size_t index = circular_index(in, object);
      if (index == 0) {
        push(&in->circular, &in->circular_len, &in->circular_cap, object);
        index = in->circular_len;
      }
      text_printf(out, "[Circular *%zu]", index);
      return;
    }
  }
  if (type->field_count == 0) {
    text_printf(out, "%s {}", type->name);
    return;
  }
  if (recurse_times > DEPTH) {
    text_printf(out, "[%s]", type->name);
    return;
  }
  recurse_times++;
  push(&in->seen, &in->seen_len, &in->seen_cap, object);
  in->current_depth = recurse_times;
  text *entries = calloc(type->field_count, sizeof *entries);
  if (entries == NULL) {
    pts_panic("RangeError", "Out of memory formatting a value for console.log");
  }
  for (uint32_t i = 0; i < type->field_count; i++) {
    format_key(&entries[i], type->fields[i].name);
    text_puts(&entries[i], ": ");
    in->indentation += 2;
    format_field(in, &entries[i], object, &type->fields[i], recurse_times);
    in->indentation -= 2;
  }
  in->seen_len--;

  text base = {0};
  size_t reference = circular_index(in, object);
  if (reference != 0) {
    text_printf(&base, "<ref *%zu> ", reference);
  }
  size_t brace = strlen(type->name) + 2; /* "Name {" */
  bool single_line = in->current_depth - recurse_times < 3 &&
                     below_break_length(entries, type->field_count, type->field_count + in->indentation + brace + (base.len ? base.len - 1 : 0) + 10);
  for (uint32_t i = 0; single_line && i < type->field_count; i++) {
    single_line = memchr(entries[i].bytes, '\n', entries[i].len) == NULL;
  }
  if (base.len) {
    text_add(out, base.bytes, base.len);
  }
  text_printf(out, "%s {", type->name);
  for (uint32_t i = 0; i < type->field_count; i++) {
    if (single_line) {
      text_puts(out, i == 0 ? " " : ", ");
    } else {
      text_puts(out, i == 0 ? "\n" : ",\n");
      for (size_t s = 0; s < in->indentation + 2; s++) {
        text_add(out, " ", 1);
      }
    }
    text_add(out, entries[i].bytes, entries[i].len);
    free(entries[i].bytes);
  }
  if (single_line) {
    text_puts(out, " }");
  } else {
    text_add(out, "\n", 1);
    for (size_t s = 0; s < in->indentation; s++) {
      text_add(out, " ", 1);
    }
    text_puts(out, "}");
  }
  free(entries);
  free(base.bytes);
}

void pts_inspect_write(FILE *out, const pts_object *object) {
  inspector in = {0};
  text result = {0};
  format_object(&in, &result, object, 0);
  fwrite(result.bytes, 1, result.len, out);
  free(result.bytes);
  free(in.seen);
  free(in.circular);
}
