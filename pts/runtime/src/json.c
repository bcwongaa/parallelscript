/*
 * JSON (M3.4, D-080): JSON.parse builds dynamic values (hash-map objects of `any` for
 * objects, any[] for arrays, numbers, strings, booleans and null) and reports malformed
 * text with V8's SyntaxError messages, positions counted in UTF-16 code units.
 * JSON.stringify walks any value through its descriptor, as `any`'s reads do.
 */
#include "pts.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The hash-map object type of JSON's objects and of the program's Record<string, any>. */
const pts_type pts_type_any_record = {
    .kind = PTS_KIND_RECORD,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Object",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_STRING,
    .element = PTS_FIELD_VALUE,
};

/* ---------------------------------------------------------------- parse */

enum { KEY_CACHE = 256 };

typedef struct parser {
  const pts_string *source;
  const char *bytes;
  size_t length;                 /* in bytes */
  size_t at;                     /* the byte being read */
  pts_string *keys[KEY_CACHE];   /* recent object keys without escapes, by a hash of their bytes: one string per repeated key */
} parser;

/* Whether the source is the text of a value that commonly reaches JSON.parse by mistake, which V8 names in full. */
static bool is_special_source(const parser *p) {
  static const char *const special[] = {"[object Object]", "undefined", "Infinity", "NaN"};
  for (size_t i = 0; i < sizeof special / sizeof special[0]; i++) {
    if (p->length == strlen(special[i]) && memcmp(p->bytes, special[i], p->length) == 0) {
      return true;
    }
  }
  return false;
}

/* How many UTF-16 code units the UTF-8 bytes [from, to) of the source encode. */
static size_t units_between(const parser *p, size_t from, size_t to) {
  size_t units = 0;
  for (size_t i = from; i < to; i++) {
    unsigned char b = (unsigned char)p->bytes[i];
    if ((b & 0xC0) != 0x80) {
      units += b >= 0xF0 ? 2 : 1;
    }
  }
  return units;
}

/* The byte offset at which the source's UTF-16 code unit `unit` starts (or the length). */
static size_t byte_at_unit(const parser *p, size_t unit) {
  size_t units = 0;
  for (size_t i = 0; i < p->length; i++) {
    unsigned char b = (unsigned char)p->bytes[i];
    if ((b & 0xC0) == 0x80) {
      continue;
    }
    if (units >= unit) {
      return i;
    }
    units += b >= 0xF0 ? 2 : 1;
  }
  return p->length;
}

/* The byte length of the UTF-8 sequence that starts at offset at. */
static size_t char_length(const parser *p, size_t at) {
  unsigned char b = (unsigned char)p->bytes[at];
  size_t n = b < 0x80 ? 1 : b >= 0xF0 ? 4 : b >= 0xE0 ? 3 : 2;
  return at + n <= p->length ? n : p->length - at;
}

/* Throws SyntaxError: what ("… in JSON"), then V8's position, line and column of the byte at. */
static _Noreturn void fail_at(const parser *p, const char *what, size_t at) {
  size_t line = 1, column = 1;
  for (size_t i = 0; i < at; i++) {
    unsigned char b = (unsigned char)p->bytes[i];
    if (b == '\n') {
      line++, column = 1;
    } else if ((b & 0xC0) != 0x80) {
      column += b >= 0xF0 ? 2 : 1;
    }
  }
  pts_throw_new(&pts_type_builtin_SyntaxError, "%s at position %zu (line %zu column %zu)", what, units_between(p, 0, at), line, column);
}

/* Throws V8's SyntaxError for an unexpected character at offset at, or the end of the text. */
static _Noreturn void unexpected(const parser *p, size_t at) {
  if (at >= p->length) {
    pts_throw_new(&pts_type_builtin_SyntaxError, "Unexpected end of JSON input");
  }
  if (is_special_source(p)) {
    pts_throw_new(&pts_type_builtin_SyntaxError, "\"%.*s\" is not valid JSON", (int)p->length, p->bytes);
  }
  /* The source in full if it is short, else ten code units on either side of the token. */
  enum { CONTEXT = 10 };
  size_t pos = units_between(p, 0, at), total = units_between(p, 0, p->length);
  size_t token = char_length(p, at);
  if (total <= 2 * CONTEXT + 1) {
    pts_throw_new(&pts_type_builtin_SyntaxError, "Unexpected token '%.*s', \"%.*s\" is not valid JSON", (int)token, p->bytes + at, (int)p->length,
                  p->bytes);
  }
  size_t start = pos < CONTEXT ? 0 : pos - CONTEXT;
  size_t end = pos < total - CONTEXT ? pos + CONTEXT : total;
  size_t from = byte_at_unit(p, start), to = byte_at_unit(p, end);
  pts_throw_new(&pts_type_builtin_SyntaxError, "Unexpected token '%.*s', %s\"%.*s\"%s is not valid JSON", (int)token, p->bytes + at,
                start > 0 ? "..." : "", (int)(to - from), p->bytes + from, end < total ? "..." : "");
}

static void skip_whitespace(parser *p) {
  while (p->at < p->length) {
    char c = p->bytes[p->at];
    if (c != ' ' && c != '\t' && c != '\n' && c != '\r') {
      return;
    }
    p->at++;
  }
}

static bool is_digit(char c) { return c >= '0' && c <= '9'; }

static pts_value parse_value(parser *p);

/* A growable byte buffer for strings with escapes. */
typedef struct buffer {
  char *data;
  size_t length, capacity;
} buffer;

static void append(buffer *b, const char *bytes, size_t n) {
  if (b->length + n > b->capacity) {
    size_t capacity = b->capacity < 64 ? 64 : b->capacity;
    while (capacity < b->length + n) {
      capacity *= 2;
    }
    char *data = realloc(b->data, capacity);
    if (data == NULL) {
      free(b->data);
      pts_panic("RangeError", "Out of memory parsing JSON");
    }
    b->data = data, b->capacity = capacity;
  }
  memcpy(b->data + b->length, bytes, n);
  b->length += n;
}

/* Appends a code point (or a lone surrogate, as WTF-8, D-014) as UTF-8. */
static void append_code_point(buffer *b, uint32_t c) {
  char bytes[4];
  size_t n;
  if (c < 0x80) {
    bytes[0] = (char)c, n = 1;
  } else if (c < 0x800) {
    bytes[0] = (char)(0xC0 | c >> 6), bytes[1] = (char)(0x80 | (c & 0x3F)), n = 2;
  } else if (c < 0x10000) {
    bytes[0] = (char)(0xE0 | c >> 12), bytes[1] = (char)(0x80 | (c >> 6 & 0x3F)), bytes[2] = (char)(0x80 | (c & 0x3F)), n = 3;
  } else {
    bytes[0] = (char)(0xF0 | c >> 18), bytes[1] = (char)(0x80 | (c >> 12 & 0x3F)), bytes[2] = (char)(0x80 | (c >> 6 & 0x3F));
    bytes[3] = (char)(0x80 | (c & 0x3F)), n = 4;
  }
  append(b, bytes, n);
}

/* The four hex digits of a \u escape at p->at, or -1 (with the offset of the bad digit in *bad). */
static int32_t hex4(const parser *p, size_t at, size_t *bad) {
  int32_t value = 0;
  for (size_t i = 0; i < 4; i++) {
    if (at + i >= p->length) {
      *bad = at + i;
      return -1;
    }
    char c = p->bytes[at + i];
    int digit = c >= '0' && c <= '9' ? c - '0' : c >= 'a' && c <= 'f' ? c - 'a' + 10 : c >= 'A' && c <= 'F' ? c - 'A' + 10 : -1;
    if (digit < 0) {
      *bad = at + i;
      return -1;
    }
    value = value * 16 + digit;
  }
  return value;
}

/* The string of bytes, from the key cache if it is an object key seen before (strings are immutable, so one serves every object). */
static pts_string *key_string(parser *p, const char *bytes, size_t n) {
  uint32_t hash = 2166136261u;
  for (size_t i = 0; i < n; i++) {
    hash = (hash ^ (unsigned char)bytes[i]) * 16777619u;
  }
  pts_string **slot = &p->keys[hash % KEY_CACHE];
  if (*slot != NULL && (*slot)->byte_len == n && memcmp((*slot)->bytes, bytes, n) == 0) {
    return *slot;
  }
  return *slot = pts_string_from_utf8(bytes, n);
}

/* A string whose opening quote is at p->at; an object key if key. */
static pts_string *parse_string(parser *p, bool key) {
  size_t start = ++p->at;
  while (p->at < p->length) { /* the common case: no escapes */
    unsigned char c = (unsigned char)p->bytes[p->at];
    if (c == '"') {
      pts_string *s = key ? key_string(p, p->bytes + start, p->at - start) : pts_string_from_utf8(p->bytes + start, p->at - start);
      p->at++;
      return s;
    }
    if (c == '\\' || c < 0x20) {
      break;
    }
    p->at++;
  }
  buffer b = {0};
  append(&b, p->bytes + start, p->at - start);
  while (p->at < p->length) {
    unsigned char c = (unsigned char)p->bytes[p->at];
    if (c == '"') {
      pts_string *s = pts_string_from_utf8(b.data, b.length);
      free(b.data);
      p->at++;
      return s;
    }
    if (c < 0x20) {
      free(b.data);
      fail_at(p, "Bad control character in string literal in JSON", p->at);
    }
    if (c != '\\') {
      append(&b, (const char *)&p->bytes[p->at], 1);
      p->at++;
      continue;
    }
    if (p->at + 1 >= p->length) {
      break; /* unterminated */
    }
    char escape = p->bytes[p->at + 1];
    const char *simple = NULL;
    switch (escape) {
    case '"': simple = "\""; break;
    case '\\': simple = "\\"; break;
    case '/': simple = "/"; break;
    case 'b': simple = "\b"; break;
    case 'f': simple = "\f"; break;
    case 'n': simple = "\n"; break;
    case 'r': simple = "\r"; break;
    case 't': simple = "\t"; break;
    case 'u': {
      size_t bad;
      int32_t unit = hex4(p, p->at + 2, &bad);
      if (unit < 0) {
        free(b.data);
        fail_at(p, "Bad Unicode escape in JSON", bad);
      }
      p->at += 6;
      uint32_t code = (uint32_t)unit;
      if (code >= 0xD800 && code < 0xDC00 && p->at + 1 < p->length && p->bytes[p->at] == '\\' && p->bytes[p->at + 1] == 'u') {
        int32_t low = hex4(p, p->at + 2, &bad);
        if (low >= 0xDC00 && low < 0xE000) { /* a surrogate pair is one code point */
          code = 0x10000 + ((code - 0xD800) << 10) + ((uint32_t)low - 0xDC00);
          p->at += 6;
        }
      }
      append_code_point(&b, code);
      continue;
    }
    default:
      free(b.data);
      fail_at(p, "Bad escaped character in JSON", p->at + 1);
    }
    append(&b, simple, 1);
    p->at += 2;
  }
  free(b.data);
  fail_at(p, "Unterminated string in JSON", p->length);
}

/* A number that starts at p->at, with V8's messages for the malformed ones. */
static pts_value parse_number(parser *p) {
  size_t start = p->at;
  if (p->bytes[p->at] == '-') {
    p->at++;
    if (p->at >= p->length || !is_digit(p->bytes[p->at])) {
      fail_at(p, "No number after minus sign in JSON", p->at);
    }
  }
  if (p->bytes[p->at] == '0') {
    p->at++;
    if (p->at < p->length && is_digit(p->bytes[p->at])) {
      fail_at(p, "Unexpected number in JSON", p->at);
    }
  } else {
    while (p->at < p->length && is_digit(p->bytes[p->at])) {
      p->at++;
    }
  }
  if (p->at < p->length && p->bytes[p->at] == '.') {
    p->at++;
    if (p->at >= p->length || !is_digit(p->bytes[p->at])) {
      fail_at(p, "Unterminated fractional number in JSON", p->at);
    }
    while (p->at < p->length && is_digit(p->bytes[p->at])) {
      p->at++;
    }
  }
  if (p->at < p->length && (p->bytes[p->at] == 'e' || p->bytes[p->at] == 'E')) {
    p->at++;
    if (p->at < p->length && (p->bytes[p->at] == '+' || p->bytes[p->at] == '-')) {
      p->at++;
    }
    if (p->at >= p->length || !is_digit(p->bytes[p->at])) {
      fail_at(p, "Exponent part is missing a number in JSON", p->at);
    }
    while (p->at < p->length && is_digit(p->bytes[p->at])) {
      p->at++;
    }
  }
  char small[64];
  size_t n = p->at - start;
  char *text = n < sizeof small ? small : malloc(n + 1);
  if (text == NULL) {
    pts_panic("RangeError", "Out of memory parsing JSON");
  }
  memcpy(text, p->bytes + start, n);
  text[n] = '\0';
  double value = strtod(text, NULL); /* correctly rounded, as JS's */
  if (text != small) {
    free(text);
  }
  return pts_value_from_number(value);
}

/* true, false or null, whose first letter is at p->at. */
static pts_value parse_literal(parser *p, const char *word, pts_value value) {
  for (size_t i = 0; word[i] != '\0'; i++, p->at++) {
    if (p->at >= p->length || p->bytes[p->at] != word[i]) {
      unexpected(p, p->at);
    }
  }
  return value;
}

static pts_value parse_array(parser *p) {
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  p->at++;
  skip_whitespace(p);
  if (p->at < p->length && p->bytes[p->at] == ']') {
    p->at++;
    return pts_value_from_object(array);
  }
  for (;;) {
    pts_value element = parse_value(p);
    pts_array_push(array, &element, 1);
    skip_whitespace(p);
    if (p->at < p->length && p->bytes[p->at] == ',') {
      p->at++;
      continue;
    }
    if (p->at < p->length && p->bytes[p->at] == ']') {
      p->at++;
      return pts_value_from_object(array);
    }
    fail_at(p, "Expected ',' or ']' after array element in JSON", p->at);
  }
}

static pts_value parse_object(parser *p) {
  pts_map *object = pts_map_new(&pts_type_any_record);
  p->at++;
  skip_whitespace(p);
  if (p->at < p->length && p->bytes[p->at] == '}') {
    p->at++;
    return pts_value_from_object(object);
  }
  for (bool first = true;; first = false) {
    skip_whitespace(p);
    if (p->at >= p->length || p->bytes[p->at] != '"') {
      fail_at(p, first ? "Expected property name or '}' in JSON" : "Expected double-quoted property name in JSON", p->at);
    }
    pts_string *key = parse_string(p, true);
    skip_whitespace(p);
    if (p->at >= p->length || p->bytes[p->at] != ':') {
      fail_at(p, "Expected ':' after property name in JSON", p->at);
    }
    p->at++;
    pts_value value = parse_value(p);
    pts_map_set(object, &key, &value); /* a repeated key keeps its place and takes the last value, as in JS */
    skip_whitespace(p);
    if (p->at < p->length && p->bytes[p->at] == ',') {
      p->at++;
      continue;
    }
    if (p->at < p->length && p->bytes[p->at] == '}') {
      p->at++;
      return pts_value_from_object(object);
    }
    fail_at(p, "Expected ',' or '}' after property value in JSON", p->at);
  }
}

static pts_value parse_value(parser *p) {
  pts_stack_check(); /* deep nesting throws RangeError rather than overflow the C stack */
  skip_whitespace(p);
  if (p->at >= p->length) {
    unexpected(p, p->at);
  }
  char c = p->bytes[p->at];
  switch (c) {
  case '"':
    return pts_value_from_string(parse_string(p, false));
  case '{':
    return parse_object(p);
  case '[':
    return parse_array(p);
  case 't':
    return parse_literal(p, "true", PTS_VALUE_TRUE);
  case 'f':
    return parse_literal(p, "false", PTS_VALUE_FALSE);
  case 'n':
    return parse_literal(p, "null", PTS_VALUE_NULL);
  default:
    if (c == '-' || is_digit(c)) {
      return parse_number(p);
    }
    unexpected(p, p->at);
  }
}

pts_value pts_json_parse(const pts_string *text) {
  parser p = {.source = text, .bytes = text->bytes, .length = text->byte_len};
  pts_value value = parse_value(&p);
  skip_whitespace(&p);
  if (p.at < p.length) {
    fail_at(&p, "Unexpected non-whitespace character after JSON", p.at);
  }
  pts_publish(); /* the arrays and objects filled after they were made */
  return value;
}

/* ---------------------------------------------------------------- stringify */

/* An object being serialized, and the key its holder reached it by: for the message about a cycle. */
typedef struct frame {
  pts_value object;
  pts_value key; /* a string, or a number for an array's index */
} frame;

typedef struct writer {
  buffer out;
  pts_value replacer; /* a function, or undefined */
  pts_array *keys;    /* a replacer array's property list, or NULL */
  const char *gap;    /* the indent of one level: "" for none */
  size_t gap_length;
  size_t depth;
  frame *stack;
  size_t stack_length, stack_capacity;
} writer;

static void put(writer *w, const char *bytes, size_t n) { append(&w->out, bytes, n); }

static void put_cstr(writer *w, const char *s) { put(w, s, strlen(s)); }

/* QuoteJSONString: escapes, \u00xx for the other control characters and for lone surrogates. */
static void put_quoted(writer *w, const pts_string *s) {
  static const char hex[] = "0123456789abcdef";
  put(w, "\"", 1);
  const unsigned char *bytes = (const unsigned char *)s->bytes;
  size_t plain = 0; /* the common case: nothing to escape */
  while (plain < s->byte_len && bytes[plain] >= 0x20 && bytes[plain] != '"' && bytes[plain] != '\\' && bytes[plain] != 0xED) {
    plain++;
  }
  if (plain == s->byte_len) {
    put(w, s->bytes, s->byte_len);
    put(w, "\"", 1);
    return;
  }
  size_t start = 0;
  for (size_t i = 0; i < s->byte_len; i++) {
    unsigned char c = bytes[i];
    const char *escape = NULL;
    char unicode[7];
    switch (c) {
    case '"': escape = "\\\""; break;
    case '\\': escape = "\\\\"; break;
    case '\b': escape = "\\b"; break;
    case '\f': escape = "\\f"; break;
    case '\n': escape = "\\n"; break;
    case '\r': escape = "\\r"; break;
    case '\t': escape = "\\t"; break;
    default:
      if (c < 0x20) {
        snprintf(unicode, sizeof unicode, "\\u00%c%c", hex[c >> 4], hex[c & 0xF]);
        escape = unicode;
      } else if (c == 0xED && i + 2 < s->byte_len && bytes[i + 1] >= 0xA0) { /* a lone surrogate (WTF-8, D-014) */
        uint32_t unit = (uint32_t)(c & 0x0F) << 12 | (uint32_t)(bytes[i + 1] & 0x3F) << 6 | (bytes[i + 2] & 0x3F);
        snprintf(unicode, sizeof unicode, "\\u%c%c%c%c", hex[unit >> 12], hex[unit >> 8 & 0xF], hex[unit >> 4 & 0xF], hex[unit & 0xF]);
        put(w, s->bytes + start, i - start);
        put_cstr(w, unicode);
        i += 2;
        start = i + 1;
        continue;
      }
    }
    if (escape != NULL) {
      put(w, s->bytes + start, i - start);
      put_cstr(w, escape);
      start = i + 1;
    }
  }
  put(w, s->bytes + start, s->byte_len - start);
  put(w, "\"", 1);
}

static void put_newline_indent(writer *w) {
  put(w, "\n", 1);
  for (size_t i = 0; i < w->depth; i++) {
    put(w, w->gap, w->gap_length);
  }
}

/* The name V8 gives an object's constructor in the message about a cycle. */
static const char *constructor_name(pts_value value) {
  const pts_type *type = pts_value_as_object(value)->type;
  return type->kind == PTS_KIND_ARRAY || type->kind == PTS_KIND_TUPLE ? "Array" : type->name != NULL ? type->name : "Object";
}

static void describe_key(buffer *b, pts_value key) {
  char text[64];
  if (pts_value_is_number(key)) {
    snprintf(text, sizeof text, "index %.0f", pts_value_as_number(key));
    append(b, text, strlen(text));
    return;
  }
  const pts_string *name = pts_value_as_string(key);
  append(b, "property '", 10);
  append(b, name->bytes, name->byte_len);
  append(b, "'", 1);
}

/* Throws V8's TypeError for a cycle that closes at the object stack[from] by key. */
static _Noreturn void circular(writer *w, size_t from, pts_value key) {
  enum { HEAD = 2, TAIL = 1 };
  buffer b = {0};
  const char *intro = "Converting circular structure to JSON\n    --> starting at object with constructor '";
  append(&b, intro, strlen(intro));
  const char *name = constructor_name(w->stack[from].object);
  append(&b, name, strlen(name));
  append(&b, "'", 1);
  size_t links = w->stack_length - from - 1; /* the objects between the start and the key that closes the circle */
  for (size_t i = 0; i < links; i++) {
    if (links > HEAD + TAIL && i == HEAD) {
      append(&b, "\n    |     ...", 14);
      i = links - TAIL - 1;
      continue;
    }
    const frame *link = &w->stack[from + 1 + i];
    append(&b, "\n    |     ", 11);
    describe_key(&b, link->key);
    const char *arrow = " -> object with constructor '";
    append(&b, arrow, strlen(arrow));
    name = constructor_name(link->object);
    append(&b, name, strlen(name));
    append(&b, "'", 1);
  }
  append(&b, "\n    --- ", 9);
  describe_key(&b, key);
  append(&b, " closes the circle", 18);
  pts_string *message = pts_string_from_utf8(b.data, b.length);
  free(b.data);
  free(w->out.data);
  pts_throw_error(pts_string_from_cstr("TypeError"), message);
}

static void push_frame(writer *w, pts_value object, pts_value key) {
  for (size_t i = 0; i < w->stack_length; i++) {
    if (w->stack[i].object == object) {
      circular(w, i, key);
    }
  }
  if (w->stack_length == w->stack_capacity) {
    size_t capacity = w->stack_capacity < 16 ? 16 : w->stack_capacity * 2;
    frame *stack = GC_REALLOC(w->stack, capacity * sizeof *stack); /* collected memory: a frame's key may be the only reference to its string */
    if (stack == NULL) {
      pts_panic("RangeError", "Out of memory in JSON.stringify");
    }
    w->stack = stack, w->stack_capacity = capacity;
  }
  w->stack[w->stack_length++] = (frame){object, key};
}

/* The toJSON function of a value, or undefined: a method of the program's classes and object literals, or a hash-map object's function. */
static bool call_to_json(pts_value value, pts_value key, pts_value *result) {
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_object *object = pts_value_as_object(value);
  const pts_type *type = object->type;
  if (type->kind == PTS_KIND_OBJECT) {
    for (uint32_t i = 0; i < type->method_count; i++) {
      if (type->methods[i].kind == PTS_METHOD && type->methods[i].dynamic != NULL && strcmp(type->methods[i].name, "toJSON") == 0) {
        *result = type->methods[i].dynamic(object, 1, &key);
        return true;
      }
    }
  }
  if (type->kind != PTS_KIND_OBJECT && type->kind != PTS_KIND_RECORD) {
    return false;
  }
  pts_value function = pts_dynamic_get(value, pts_value_from_string(pts_string_from_cstr("toJSON")));
  if (!pts_value_is_object(function) || pts_value_as_object(function)->type->kind != PTS_KIND_FUNCTION) {
    return false;
  }
  PTS_STATIC_STRING(text, 1, 6, 6, "toJSON");
  *result = pts_dynamic_call(function, text, 1, &key);
  return true;
}

static bool serialize(writer *w, pts_value holder, pts_value key, pts_value value);

/* The elements of an array or tuple, in order. */
static void serialize_array(writer *w, pts_value value) {
  pts_object *object = pts_value_as_object(value);
  bool tuple = object->type->kind == PTS_KIND_TUPLE;
  size_t length = tuple ? pts_tuple_length(object) : pts_array_length_of((pts_array *)object);
  if (length == 0) {
    put(w, "[]", 2);
    return;
  }
  put(w, "[", 1);
  w->depth++;
  for (size_t i = 0; i < length; i++) {
    if (i > 0) {
      put(w, ",", 1);
    }
    if (w->gap_length > 0) {
      put_newline_indent(w);
    }
    pts_value element;
    if (tuple) {
      const pts_field *field = &object->type->fields[i];
      element = pts_value_of_slot(field->kind, (const char *)object + field->offset);
    } else {
      if (i >= pts_array_length_of((pts_array *)object)) { /* a replacer shortened it */
        element = PTS_VALUE_UNDEFINED;
      } else {
        element = pts_array_element((pts_array *)object, (double)i);
      }
    }
    if (!serialize(w, value, pts_value_from_number((double)i), element)) {
      put(w, "null", 4);
    }
  }
  w->depth--;
  if (w->gap_length > 0) {
    put_newline_indent(w);
  }
  put(w, "]", 1);
}

/* Writes one property of an object (value), unless it is undefined or a function; returns whether it wrote it. */
static bool serialize_property(writer *w, pts_value object, pts_value key, const char *name, size_t name_length, pts_value value, bool first) {
  size_t mark = w->out.length;
  if (!first) {
    put(w, ",", 1);
  }
  if (w->gap_length > 0) {
    put_newline_indent(w);
  }
  pts_string name_string = {&pts_string_type, 0, (uint32_t)name_length, 0, (char *)name};
  put_quoted(w, &name_string);
  put(w, w->gap_length > 0 ? ": " : ":", w->gap_length > 0 ? 2 : 1);
  if (!serialize(w, object, key, value)) {
    w->out.length = mark; /* undefined and functions are left out */
    return false;
  }
  return true;
}

/*
 * The own enumerable properties of an object, a hash-map object, a map or a set, as
 * Object.keys lists them (D-077) and reads through `any` see them, or the replacer's list.
 * An object's fields and a hash-map object's entries are read in place.
 */
static void serialize_object(writer *w, pts_value value) {
  bool any = false;
  put(w, "{", 1);
  w->depth++;
  pts_object *object = pts_value_as_object(value);
  const pts_type *type = object->type;
  if (w->keys == NULL && type->kind == PTS_KIND_OBJECT && !pts_instance_of(object, &pts_type_builtin_Error)) {
    for (uint32_t i = 0; i < type->field_count; i++) {
      const pts_field *field = &type->fields[i];
      pts_value key = PTS_VALUE_UNDEFINED; /* made only for a toJSON, a replacer or a getter */
      pts_value property;
      if (field->name[0] == '#' || pts_name_is_symbol(field->name)) {
        continue; /* private, or a symbol's (D-111) */
      }
      if (pts_field_is_accessor(field->kind)) { /* an object literal's getter */
        key = pts_value_from_string(pts_string_from_cstr(field->name));
        property = pts_dynamic_get(value, key);
      } else {
        property = pts_value_of_slot(field->kind, (const char *)object + field->offset);
        if (field->kind == PTS_FIELD_VALUE && property == PTS_VALUE_ABSENT) {
          continue; /* an optional property left out */
        }
      }
      if (key == PTS_VALUE_UNDEFINED && (w->replacer != PTS_VALUE_UNDEFINED || pts_value_is_object(property))) {
        key = pts_value_from_string(pts_string_from_cstr(field->name));
      }
      any |= serialize_property(w, value, key, field->name, strlen(field->name), property, !any);
    }
  } else if (w->keys == NULL && type->kind == PTS_KIND_RECORD) {
    pts_map_iterator entries = pts_record_iterate((pts_map *)object);
    while (pts_map_next(&entries)) {
      pts_string *name = PTS_MAP_ITERATOR_KEY(pts_string *, entries);
      pts_value property = pts_value_of_slot(type->element, &entries.value);
      any |= serialize_property(w, value, pts_value_from_string(name), name->bytes, name->byte_len, property, !any);
    }
  } else {
    pts_array *keys = w->keys != NULL ? w->keys : (pts_array *)pts_value_as_object(pts_value_keys(value));
    size_t length = pts_array_length_of(keys);
    for (size_t i = 0; i < length; i++) {
      pts_value key = pts_array_element(keys, (double)i);
      const pts_string *name = pts_value_as_string(key);
      any |= serialize_property(w, value, key, name->bytes, name->byte_len, pts_dynamic_get(value, key), !any);
    }
  }
  w->depth--;
  if (any && w->gap_length > 0) {
    put_newline_indent(w);
  }
  put(w, "}", 1);
}

/* SerializeJSONProperty: writes value (holder's property key) and returns true, or false for undefined and functions. */
static bool serialize(writer *w, pts_value holder, pts_value key, pts_value value) {
  pts_stack_check();
  pts_value key_string = pts_value_is_number(key) ? pts_value_from_string(pts_value_to_string(key)) : key;
  pts_value replaced;
  if (pts_value_is_object(value) && call_to_json(value, key_string, &replaced)) {
    value = replaced;
  }
  if (w->replacer != PTS_VALUE_UNDEFINED) {
    pts_value args[] = {key_string, value};
    PTS_STATIC_STRING(text, 1, 8, 8, "replacer");
    (void)holder; /* `this`, which only methods take in ParallelScript */
    value = pts_dynamic_call(w->replacer, text, 2, args);
  }
  if (value == PTS_VALUE_NULL) {
    put(w, "null", 4);
    return true;
  }
  if (value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE) {
    put_cstr(w, value == PTS_VALUE_TRUE ? "true" : "false");
    return true;
  }
  if (pts_value_is_number(value)) {
    double number = pts_value_as_number(value);
    if (number != number || __builtin_isinf(number)) {
      put(w, "null", 4);
      return true;
    }
    char text[PTS_NUMBER_BUF_SIZE];
    put(w, text, pts_number_format(number, text));
    return true;
  }
  if (!pts_value_is_object(value)) {
    return false; /* undefined */
  }
  const pts_type *type = pts_value_as_object(value)->type;
  char text[32];
  switch (type->kind) {
  case PTS_KIND_STRING:
    put_quoted(w, pts_value_as_string(value));
    return true;
  case PTS_KIND_INT64:
    snprintf(text, sizeof text, "%lld", (long long)((const pts_boxed_int64 *)pts_value_as_object(value))->value);
    put_cstr(w, text);
    return true;
  case PTS_KIND_UINT64:
    snprintf(text, sizeof text, "%llu", (unsigned long long)((const pts_boxed_uint64 *)pts_value_as_object(value))->value);
    put_cstr(w, text);
    return true;
  case PTS_KIND_FUNCTION:
    return false;
  case PTS_KIND_DATE: {
    pts_value json = pts_date_to_json((pts_date *)pts_value_as_object(value));
    if (json == PTS_VALUE_NULL) {
      put_cstr(w, "null");
    } else {
      put_quoted(w, pts_value_as_string(json));
    }
    return true;
  }
  case PTS_KIND_ARRAY:
  case PTS_KIND_TUPLE:
    push_frame(w, value, key);
    serialize_array(w, value);
    w->stack_length--;
    return true;
  default:
    push_frame(w, value, key);
    serialize_object(w, value);
    w->stack_length--;
    return true;
  }
}

/* A replacer array's property list: its strings and numbers as strings, each once. */
static pts_array *property_list(pts_value replacer) {
  pts_array *list = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  pts_object *object = pts_value_as_object(replacer);
  size_t length = object->type->kind == PTS_KIND_TUPLE ? pts_tuple_length(object) : pts_array_length_of((pts_array *)object);
  for (size_t i = 0; i < length; i++) {
    pts_value item = pts_dynamic_get(replacer, pts_value_from_number((double)i));
    if (!pts_value_is_number(item) && !pts_value_is_string(item)) {
      continue;
    }
    pts_value name = pts_value_from_string(pts_value_to_string(item));
    bool seen = false;
    for (size_t j = 0; j < pts_array_length_of(list) && !seen; j++) {
      seen = pts_value_strict_equals(pts_array_element(list, (double)j), name);
    }
    if (!seen) {
      pts_array_push(list, &name, 1);
    }
  }
  return list;
}

pts_value pts_json_stringify(pts_value value, pts_value replacer, pts_value space) {
  writer w = {.replacer = PTS_VALUE_UNDEFINED, .gap = ""};
  if (pts_value_is_object(replacer)) {
    pts_kind kind = pts_value_as_object(replacer)->type->kind;
    if (kind == PTS_KIND_FUNCTION) {
      w.replacer = replacer;
    } else if (kind == PTS_KIND_ARRAY || kind == PTS_KIND_TUPLE) {
      w.keys = property_list(replacer);
    }
  }
  char spaces[11] = "          ";
  pts_string *gap = NULL;
  if (pts_value_is_number(space) || (pts_value_is_object(space) && pts_value_as_object(space)->type->kind == PTS_KIND_INT64)) {
    double count = pts_value_to_number(space);
    w.gap_length = count >= 10 ? 10 : count >= 1 ? (size_t)count : 0;
    w.gap = spaces;
  } else if (pts_value_is_string(space)) {
    gap = pts_string_slice(pts_value_as_string(space), 0, 10);
    w.gap = gap->bytes, w.gap_length = gap->byte_len;
  }
  pts_map *wrapper = pts_map_new(&pts_type_any_record); /* the holder of the value, as in JS */
  PTS_STATIC_STRING(empty, 1, 0, 0, "");
  pts_map_set(wrapper, &empty, &value);
  bool written = serialize(&w, pts_value_from_object(wrapper), pts_value_from_string(empty), value);
  if (!written) {
    free(w.out.data);
    return PTS_VALUE_UNDEFINED;
  }
  pts_string *result = pts_string_from_utf8(w.out.data, w.out.length);
  free(w.out.data);
  return pts_value_from_string(result);
}

/* ---------------------------------------------------------------- revivers */

/* The code of a reviver, converted to the function type (key: string, value: any) => any. */
typedef pts_value (*reviver_code)(pts_closure *, pts_string *, pts_value);

static pts_value revive(pts_closure *reviver, pts_string *name, pts_value value);

/*
 * The children of a parsed array or object, each replaced by what the reviver makes of it,
 * or left out (a hole in an array, a deleted key) where it makes undefined
 * (InternalizeJSONProperty, ECMAScript 25.5.1.1).
 */
static void revive_children(pts_closure *reviver, pts_value value) {
  if (!pts_value_is_object(value)) {
    return;
  }
  pts_object *object = pts_value_as_object(value);
  if (object->type == &pts_array_types[PTS_FIELD_VALUE]) {
    pts_array *array = (pts_array *)object;
    for (size_t i = 0; i < pts_array_length_of(array); i++) {
      pts_value element = pts_value_present(pts_array_element(array, (double)i));
      pts_value revived = revive(reviver, pts_number_to_string((double)i), element);
      if (revived == PTS_VALUE_UNDEFINED) {
        revived = PTS_VALUE_ABSENT;
      }
      pts_array_set(array, (double)i, &revived);
    }
    return;
  }
  if (object->type != &pts_type_any_record) {
    return;
  }
  pts_map *record = (pts_map *)object;
  size_t count = (size_t)pts_map_size(record), n = 0;
  pts_string **keys = GC_MALLOC((count ? count : 1) * sizeof *keys); /* the keys as they were, as JS takes them first; collected memory, which the collector scans */
  if (keys == NULL) {
    abort();
  }
  pts_map_iterator entries = pts_record_iterate(record);
  while (pts_map_next(&entries) && n < count) {
    keys[n++] = PTS_MAP_ITERATOR_KEY(pts_string *, entries);
  }
  for (size_t i = 0; i < n; i++) {
    pts_value revived = revive(reviver, keys[i], pts_map_get_value(record, &keys[i]));
    if (revived == PTS_VALUE_UNDEFINED) {
      pts_map_delete(record, &keys[i]);
    } else {
      pts_map_set(record, &keys[i], &revived);
    }
  }
}

static pts_value revive(pts_closure *reviver, pts_string *name, pts_value value) {
  revive_children(reviver, value);
  return ((reviver_code)reviver->code)(reviver, name, value);
}

pts_value pts_json_parse_revived(const pts_string *text, pts_value reviver) {
  pts_value root = pts_json_parse(text);
  pts_value revived = revive((pts_closure *)pts_value_as_object(reviver), pts_string_from_cstr(""), root);
  pts_publish(); /* the reviver's values, stored into the parsed objects after pts_json_parse published them */
  return revived;
}
