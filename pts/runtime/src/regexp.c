/*
 * The core of RegExp (D-132): a JS pattern is translated to PCRE2's syntax and compiled
 * by its 8-bit library in UTF mode; matches are reported in JS's UTF-16 indices.
 *
 * The translation keeps JS's meaning where the two differ: `.` and `^`/`$` with the m flag
 * know JS's four line terminators; `\v` is a character, not a class; escaped letters JS
 * doesn't know are the letters themselves (PCRE2 gives `\a`, `\e`, `\h` … meanings); `[`
 * in a class is literal; a backreference is a group's number; named groups are numbered
 * groups with their names kept here, so any JS name works. It also finds the errors V8
 * reports, with V8's messages; anything PCRE2 rejects besides is a SyntaxError with PCRE2's.
 * Compiled code is collected memory with no pointers in it; threads share it.
 */
#include "pts.h"
#include "string_internal.h"

#define PCRE2_CODE_UNIT_WIDTH 8
#define PCRE2_STATIC
#include <pcre2.h>

#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct pts_regex {
  pcre2_code *code;
  pts_string **names; /* names[g] for group g, NULL if unnamed; groups + 1 entries */
  uint32_t groups;
  unsigned flags;
};

/* ---------------------------------------------------------------- memory */

static void *collected_malloc(size_t size, void *data) {
  (void)data;
  return GC_MALLOC_ATOMIC(size);
}

static void collected_free(void *block, void *data) {
  (void)data;
  if (block != NULL) {
    GC_FREE(block);
  }
}

static pcre2_general_context *shared_context;

static void make_general_context(void) { shared_context = pcre2_general_context_create(collected_malloc, collected_free, NULL); }

/* The memory functions every PCRE2 call allocates through: the collector's, made once. */
static pcre2_general_context *general_context(void) {
  static pthread_once_t once = PTHREAD_ONCE_INIT;
  pthread_once(&once, make_general_context);
  return shared_context;
}

/* ---------------------------------------------------------------- the translation */

/* A growing buffer of the translated pattern (malloc'd: it lives only while compiling). */
typedef struct buffer {
  char *bytes;
  size_t len, cap;
} buffer;

static void put(buffer *b, const char *bytes, size_t len) {
  if (b->len + len + 1 > b->cap) {
    b->cap = (b->len + len + 1) * 2;
    b->bytes = realloc(b->bytes, b->cap);
    if (b->bytes == NULL) {
      pts_panic("RangeError", "Out of memory compiling a regular expression");
    }
  }
  memcpy(b->bytes + b->len, bytes, len);
  b->len += len;
  b->bytes[b->len] = '\0';
}

static void puts_(buffer *b, const char *text) { put(b, text, strlen(text)); }

static void printf_(buffer *b, const char *format, ...) __attribute__((format(printf, 2, 3)));
static void printf_(buffer *b, const char *format, ...) {
  char text[64];
  va_list args;
  va_start(args, format);
  int n = vsnprintf(text, sizeof text, format, args);
  va_end(args);
  put(b, text, (size_t)n);
}

enum { MAX_DEPTH = 256 };

/* What a group's closing parenthesis restores: whether it was a lookbehind or lookahead, and the scoped flags. */
typedef struct group {
  enum { GROUP_PLAIN, GROUP_LOOKAHEAD, GROUP_LOOKBEHIND } kind;
  bool multiline, dot_all;
} group;

typedef struct translation {
  const char *p; /* the JS pattern's bytes */
  size_t len, i;
  bool unicode, multiline, dot_all;
  uint32_t groups;     /* capturing groups in the whole pattern */
  pts_string **names;  /* their names, by number (from the scan before translating) */
  buffer out;
  const char *error;   /* V8's message for the first error found */
} translation;

static bool fail(translation *t, const char *message) {
  if (t->error == NULL) {
    t->error = message;
  }
  return false;
}

static bool at_end(const translation *t) { return t->i >= t->len; }
static char peek(const translation *t, size_t ahead) { return t->i + ahead < t->len ? t->p[t->i + ahead] : '\0'; }

static int hex_value(char c) {
  if (c >= '0' && c <= '9') {
    return c - '0';
  }
  if (c >= 'a' && c <= 'f') {
    return c - 'a' + 10;
  }
  if (c >= 'A' && c <= 'F') {
    return c - 'A' + 10;
  }
  return -1;
}

/* Reads count hex digits at the cursor into *value, or leaves the cursor and returns false. */
static bool hex_digits(translation *t, int count, uint32_t *value) {
  uint32_t v = 0;
  for (int k = 0; k < count; k++) {
    int digit = hex_value(peek(t, (size_t)k));
    if (digit < 0) {
      return false;
    }
    v = v * 16 + (uint32_t)digit;
  }
  t->i += (size_t)count;
  *value = v;
  return true;
}

static bool is_surrogate(uint32_t c) { return c >= 0xD800 && c <= 0xDFFF; }

/* The length of the UTF-8 (or WTF-8) character whose lead byte is at the cursor. */
static size_t char_length(const translation *t) {
  unsigned char lead = (unsigned char)t->p[t->i];
  size_t n = lead < 0x80 ? 1 : lead < 0xE0 ? 2 : lead < 0xF0 ? 3 : 4;
  return t->i + n <= t->len ? n : t->len - t->i;
}

/* A group name at the cursor, up to its '>': JS's identifier characters (D-132 takes any but '>'). */
static pts_string *group_name(translation *t) {
  size_t start = t->i;
  while (!at_end(t) && t->p[t->i] != '>' && t->p[t->i] != ')') {
    char c = t->p[t->i];
    bool ascii_ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '$' || (c >= '0' && c <= '9') || (unsigned char)c >= 0x80;
    if (!ascii_ok || (t->i == start && c >= '0' && c <= '9')) {
      return NULL;
    }
    t->i += char_length(t);
  }
  if (at_end(t) || t->p[t->i] != '>' || t->i == start) {
    return NULL;
  }
  pts_string *name = pts_string_from_utf8(t->p + start, t->i - start);
  t->i++;
  return name;
}

/* scan counts the capturing groups and collects their names before the translation, which needs them for backreferences. */
static bool scan_groups(translation *t) {
  size_t capacity = 8;
  t->names = GC_MALLOC(capacity * sizeof *t->names);
  bool in_class = false;
  for (t->i = 0; t->i < t->len;) {
    char c = t->p[t->i];
    if (c == '\\') {
      t->i += 2;
    } else if (in_class) {
      in_class = c != ']';
      t->i++;
    } else if (c == '[') {
      in_class = true;
      t->i++;
    } else if (c == '(' && peek(t, 1) != '?') {
      t->groups++;
      t->i++;
    } else if (c == '(' && peek(t, 2) == '<' && peek(t, 3) != '=' && peek(t, 3) != '!') {
      t->i += 3;
      pts_string *name = group_name(t);
      if (name == NULL) {
        return fail(t, "Invalid capture group name");
      }
      t->groups++;
      if (t->groups + 1 > capacity) {
        pts_string **bigger = GC_MALLOC(2 * capacity * sizeof *bigger);
        memcpy(bigger, t->names, capacity * sizeof *bigger);
        t->names = bigger;
        capacity *= 2;
      }
      for (uint32_t g = 1; g < t->groups; g++) {
        if (t->names[g] != NULL && pts_string_equals(t->names[g], name)) {
          return fail(t, "Duplicate capture group name");
        }
      }
      t->names[t->groups] = name;
    } else {
      t->i++;
    }
  }
  if (t->groups + 1 > capacity) {
    pts_string **bigger = GC_MALLOC((t->groups + 1) * sizeof *bigger);
    memcpy(bigger, t->names, capacity * sizeof *bigger);
    t->names = bigger;
  }
  t->i = 0;
  return true;
}

/* What a class atom or escape was: a character (code point known), a class like \d, or something else. */
typedef enum { ATOM_CHAR, ATOM_CLASS, ATOM_ASSERTION, ATOM_OTHER } atom_kind;

typedef struct atom {
  atom_kind kind;
  uint32_t code_point;
  bool unmatchable; /* a lone surrogate, which no UTF-8 subject holds as a character */
} atom;

/* emit_char writes code point c as an escape PCRE2 reads as that character. */
static void emit_char(translation *t, uint32_t c) { printf_(&t->out, "\\x{%x}", c); }

/* A legacy octal escape (\0, \12, \377) of non-Unicode patterns: up to three octal digits, at most 0377. */
static uint32_t octal_escape(translation *t) {
  uint32_t value = 0;
  for (int k = 0; k < 3 && peek(t, 0) >= '0' && peek(t, 0) <= '7'; k++) {
    uint32_t next = value * 8 + (uint32_t)(peek(t, 0) - '0');
    if (next > 0377) {
      break;
    }
    value = next;
    t->i++;
  }
  return value;
}

/* \u escapes: \uXXXX (a surrogate pair of two of them joined), or \u{…} in Unicode patterns. */
static bool unicode_escape(translation *t, atom *a) {
  uint32_t value;
  if (t->unicode && peek(t, 0) == '{') {
    t->i++;
    value = 0;
    size_t digits = 0;
    int digit;
    while ((digit = hex_value(peek(t, 0))) >= 0) {
      value = value * 16 + (uint32_t)digit;
      if (value > 0x10FFFF) {
        return fail(t, "Invalid Unicode escape");
      }
      digits++;
      t->i++;
    }
    if (digits == 0 || peek(t, 0) != '}') {
      return fail(t, "Invalid Unicode escape");
    }
    t->i++;
  } else if (!hex_digits(t, 4, &value)) {
    if (t->unicode) {
      return fail(t, "Invalid Unicode escape");
    }
    *a = (atom){ATOM_CHAR, 'u', false};
    puts_(&t->out, "u");
    return true;
  }
  if (value >= 0xD800 && value <= 0xDBFF && peek(t, 0) == '\\' && peek(t, 1) == 'u') {
    size_t mark = t->i;
    t->i += 2;
    uint32_t low;
    if (hex_digits(t, 4, &low) && low >= 0xDC00 && low <= 0xDFFF) {
      value = 0x10000 + ((value - 0xD800) << 10) + (low - 0xDC00);
    } else {
      t->i = mark;
    }
  }
  *a = (atom){ATOM_CHAR, value, is_surrogate(value)};
  if (!a->unmatchable) {
    emit_char(t, value);
  }
  return true;
}

/* Turns a JS property expression (L, Letter, gc=Lu, Script=Greek, scx=Latn, Alphabetic) into PCRE2's. */
static bool property_escape(translation *t, bool negated) {
  static const char *const long_names[][2] = {
      {"Letter", "L"}, {"Cased_Letter", "LC"}, {"Uppercase_Letter", "Lu"}, {"Lowercase_Letter", "Ll"}, {"Titlecase_Letter", "Lt"},
      {"Modifier_Letter", "Lm"}, {"Other_Letter", "Lo"}, {"Mark", "M"}, {"Combining_Mark", "M"}, {"Nonspacing_Mark", "Mn"},
      {"Spacing_Mark", "Mc"}, {"Enclosing_Mark", "Me"}, {"Number", "N"}, {"Decimal_Number", "Nd"}, {"digit", "Nd"},
      {"Letter_Number", "Nl"}, {"Other_Number", "No"}, {"Punctuation", "P"}, {"punct", "P"}, {"Connector_Punctuation", "Pc"},
      {"Dash_Punctuation", "Pd"}, {"Open_Punctuation", "Ps"}, {"Close_Punctuation", "Pe"}, {"Initial_Punctuation", "Pi"},
      {"Final_Punctuation", "Pf"}, {"Other_Punctuation", "Po"}, {"Symbol", "S"}, {"Math_Symbol", "Sm"}, {"Currency_Symbol", "Sc"},
      {"Modifier_Symbol", "Sk"}, {"Other_Symbol", "So"}, {"Separator", "Z"}, {"Space_Separator", "Zs"}, {"Line_Separator", "Zl"},
      {"Paragraph_Separator", "Zp"}, {"Other", "C"}, {"Control", "Cc"}, {"cntrl", "Cc"}, {"Format", "Cf"}, {"Surrogate", "Cs"},
      {"Private_Use", "Co"}, {"Unassigned", "Cn"},
  };
  if (peek(t, 0) != '{') {
    return fail(t, "Invalid property name");
  }
  size_t start = ++t->i;
  while (!at_end(t) && t->p[t->i] != '}') {
    t->i++;
  }
  if (at_end(t)) {
    return fail(t, "Invalid property name");
  }
  size_t len = t->i - start;
  t->i++;
  char text[128];
  if (len == 0 || len >= sizeof text) {
    return fail(t, "Invalid property name");
  }
  memcpy(text, t->p + start, len);
  text[len] = '\0';
  const char *prefix = "";
  char *value = text;
  char *equals = strchr(text, '=');
  if (equals != NULL) {
    *equals = '\0';
    value = equals + 1;
    if (strcmp(text, "General_Category") == 0 || strcmp(text, "gc") == 0) {
      prefix = "";
    } else if (strcmp(text, "Script") == 0 || strcmp(text, "sc") == 0) {
      prefix = "sc:";
    } else if (strcmp(text, "Script_Extensions") == 0 || strcmp(text, "scx") == 0) {
      prefix = "scx:";
    } else {
      return fail(t, "Invalid property name");
    }
  }
  for (size_t k = 0; k < sizeof long_names / sizeof *long_names; k++) {
    if (strcmp(value, long_names[k][0]) == 0) {
      value = (char *)long_names[k][1];
      break;
    }
  }
  printf_(&t->out, "\\%c{%s", negated ? 'P' : 'p', prefix);
  puts_(&t->out, value);
  puts_(&t->out, "}");
  return true;
}

/* A decimal escape: a backreference outside classes when the pattern has that group, else (Annex B) an octal or the digits themselves. */
static bool decimal_escape(translation *t, bool in_class, atom *a) {
  if (peek(t, 0) == '0' && !(peek(t, 1) >= '0' && peek(t, 1) <= '9')) {
    t->i++;
    *a = (atom){ATOM_CHAR, 0, false};
    emit_char(t, 0);
    return true;
  }
  size_t mark = t->i;
  uint32_t number = 0;
  while (peek(t, 0) >= '0' && peek(t, 0) <= '9' && number < 100000) {
    number = number * 10 + (uint32_t)(peek(t, 0) - '0');
    t->i++;
  }
  if (!in_class && number >= 1 && number <= t->groups) {
    *a = (atom){ATOM_OTHER, 0, false};
    printf_(&t->out, "\\g{%u}", number);
    return true;
  }
  if (t->unicode) {
    return fail(t, in_class ? "Invalid class escape" : "Invalid escape");
  }
  t->i = mark;
  uint32_t value = peek(t, 0) >= '8' ? (uint32_t)t->p[t->i++] : octal_escape(t);
  *a = (atom){ATOM_CHAR, value, false};
  emit_char(t, value);
  return true;
}

/* A \k<name> backreference, or (in a non-Unicode pattern without named groups) the letter k. */
static bool named_reference(translation *t, atom *a) {
  bool has_names = false;
  for (uint32_t g = 1; g <= t->groups; g++) {
    has_names = has_names || t->names[g] != NULL;
  }
  if (!t->unicode && !has_names) {
    *a = (atom){ATOM_CHAR, 'k', false};
    puts_(&t->out, "k");
    return true;
  }
  if (peek(t, 0) != '<') {
    return fail(t, "Invalid named reference");
  }
  t->i++;
  pts_string *name = group_name(t);
  if (name == NULL) {
    return fail(t, "Invalid capture group name");
  }
  for (uint32_t g = 1; g <= t->groups; g++) {
    if (t->names[g] != NULL && pts_string_equals(t->names[g], name)) {
      *a = (atom){ATOM_OTHER, 0, false};
      printf_(&t->out, "\\g{%u}", g);
      return true;
    }
  }
  return fail(t, "Invalid named capture referenced");
}

/* escape translates the escape whose backslash is at the cursor, inside a class or not, and says what it was. */
static bool escape(translation *t, bool in_class, atom *a) {
  t->i++;
  if (at_end(t)) {
    return fail(t, "\\ at end of pattern");
  }
  char e = t->p[t->i];
  switch (e) {
  case 'd':
  case 'D':
  case 'w':
  case 'W':
  case 's':
  case 'S':
    t->i++;
    *a = (atom){ATOM_CLASS, 0, false};
    printf_(&t->out, "\\%c", e);
    return true;
  case 'b':
  case 'B':
    t->i++;
    if (in_class && e == 'b') {
      *a = (atom){ATOM_CHAR, 8, false};
      emit_char(t, 8);
      return true;
    }
    if (in_class) {
      if (t->unicode) {
        return fail(t, "Invalid class escape");
      }
      *a = (atom){ATOM_CHAR, 'B', false};
      puts_(&t->out, "B");
      return true;
    }
    *a = (atom){ATOM_ASSERTION, 0, false};
    printf_(&t->out, "\\%c", e);
    return true;
  case 'f':
  case 'n':
  case 'r':
  case 't':
  case 'v': {
    t->i++;
    uint32_t c = e == 'f' ? '\f' : e == 'n' ? '\n' : e == 'r' ? '\r' : e == 't' ? '\t' : '\v';
    *a = (atom){ATOM_CHAR, c, false};
    emit_char(t, c);
    return true;
  }
  case 'c': {
    char letter = peek(t, 1);
    if ((letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')) {
      t->i += 2;
      *a = (atom){ATOM_CHAR, (uint32_t)letter % 32, false};
      emit_char(t, a->code_point);
      return true;
    }
    if (t->unicode) {
      return fail(t, "Invalid unicode escape");
    }
    *a = (atom){ATOM_CHAR, '\\', false}; /* Annex B: a backslash, then c as itself */
    puts_(&t->out, "\\\\");
    return true;
  }
  case 'x': {
    t->i++;
    uint32_t value;
    if (hex_digits(t, 2, &value)) {
      *a = (atom){ATOM_CHAR, value, false};
      emit_char(t, value);
      return true;
    }
    if (t->unicode) {
      return fail(t, "Invalid escape");
    }
    *a = (atom){ATOM_CHAR, 'x', false};
    puts_(&t->out, "x");
    return true;
  }
  case 'u':
    t->i++;
    return unicode_escape(t, a);
  case 'k':
    t->i++;
    if (in_class) {
      if (t->unicode) {
        return fail(t, "Invalid class escape");
      }
      *a = (atom){ATOM_CHAR, 'k', false};
      puts_(&t->out, "k");
      return true;
    }
    return named_reference(t, a);
  case 'p':
  case 'P':
    t->i++;
    if (!t->unicode) {
      *a = (atom){ATOM_CHAR, (uint32_t)e, false};
      printf_(&t->out, "%c", e);
      return true;
    }
    *a = (atom){ATOM_CLASS, 0, false};
    return property_escape(t, e == 'P');
  case '-':
    t->i++;
    *a = (atom){ATOM_CHAR, '-', false};
    puts_(&t->out, "\\-");
    return true;
  case '/':
    t->i++;
    *a = (atom){ATOM_CHAR, '/', false};
    puts_(&t->out, "/");
    return true;
  default:
    break;
  }
  if (e >= '0' && e <= '9') {
    return decimal_escape(t, in_class, a);
  }
  if (strchr("^$\\.*+?()[]{}|", e) != NULL) {
    t->i++;
    *a = (atom){ATOM_CHAR, (uint32_t)e, false};
    printf_(&t->out, "\\%c", e);
    return true;
  }
  if (t->unicode) {
    return fail(t, "Invalid escape");
  }
  /* Annex B: any other character escapes to itself; a letter PCRE2 would read as an escape is written bare. */
  size_t n = char_length(t);
  if ((unsigned char)e < 0x80 && !((e >= 'a' && e <= 'z') || (e >= 'A' && e <= 'Z') || (e >= '0' && e <= '9'))) {
    puts_(&t->out, "\\");
  }
  unsigned length;
  uint32_t c = (unsigned char)e < 0x80 ? (uint32_t)e : pts_utf8_decode(t->p + t->i, &length);
  *a = (atom){ATOM_CHAR, c, is_surrogate(c)};
  if (!a->unmatchable) {
    put(&t->out, t->p + t->i, n);
  }
  t->i += n;
  return true;
}

/* A literal character at the cursor: copied, unless it is a lone surrogate (which no UTF-8 subject holds). */
static void literal(translation *t, bool in_class, atom *a) {
  size_t n = char_length(t);
  unsigned length;
  uint32_t c = (unsigned char)t->p[t->i] < 0x80 ? (uint32_t)(unsigned char)t->p[t->i] : pts_utf8_decode(t->p + t->i, &length);
  *a = (atom){ATOM_CHAR, c, is_surrogate(c)};
  if (a->unmatchable) {
    t->i += n;
    return;
  }
  if (in_class && c == '[') {
    puts_(&t->out, "\\[");
  } else {
    put(&t->out, t->p + t->i, n);
  }
  t->i += n;
}

static bool class_atom(translation *t, atom *a) {
  if (t->p[t->i] == '\\') {
    return escape(t, true, a);
  }
  literal(t, true, a);
  return true;
}

/* character_class translates [...] (the cursor at its '['). */
static bool character_class(translation *t) {
  t->i++;
  puts_(&t->out, "[");
  if (peek(t, 0) == '^') {
    t->i++;
    puts_(&t->out, "^");
  }
  while (!at_end(t) && t->p[t->i] != ']') {
    atom from;
    size_t start = t->out.len;
    if (!class_atom(t, &from)) {
      return false;
    }
    if (peek(t, 0) != '-' || peek(t, 1) == ']' || t->i + 1 >= t->len) {
      continue;
    }
    t->i++;
    size_t dash = t->out.len;
    puts_(&t->out, "-");
    atom to;
    if (!class_atom(t, &to)) {
      return false;
    }
    if (from.kind != ATOM_CHAR || to.kind != ATOM_CHAR) {
      if (t->unicode) {
        return fail(t, "Invalid character class");
      }
      /* Annex B: [\d-z] is \d, '-' and z; PCRE2 rejects the range, so the '-' becomes "\-". */
      size_t second = t->out.len - (dash + 1);
      char *saved = malloc(second + 1);
      memcpy(saved, t->out.bytes + dash + 1, second);
      t->out.len = dash;
      puts_(&t->out, "\\-");
      put(&t->out, saved, second);
      free(saved);
      continue;
    }
    if (from.code_point > to.code_point) {
      return fail(t, "Range out of order in character class");
    }
    if (from.unmatchable || to.unmatchable) {
      /* A range with a surrogate end: keep the parts that UTF-8 characters can be. */
      t->out.len = start;
      t->out.bytes[start] = '\0';
      uint32_t lo = from.code_point, hi = to.code_point;
      if (lo < 0xD800) {
        printf_(&t->out, "\\x{%x}-\\x{%x}", lo, hi < 0xD800 ? hi : 0xD7FF);
      }
      if (hi > 0xDFFF) {
        printf_(&t->out, "\\x{%x}-\\x{%x}", lo > 0xE000 ? lo : 0xE000, hi);
      }
    }
  }
  if (at_end(t)) {
    return fail(t, "Unterminated character class");
  }
  t->i++;
  puts_(&t->out, "]");
  return true;
}

static void line_start(translation *t) { puts_(&t->out, t->multiline ? "(?<![^\\n\\r\\x{2028}\\x{2029}])" : "^"); }
static void line_end(translation *t) { puts_(&t->out, t->multiline ? "(?![^\\n\\r\\x{2028}\\x{2029}])" : "\\z"); }
static void dot(translation *t) { puts_(&t->out, t->dot_all ? "(?s:.)" : "[^\\n\\r\\x{2028}\\x{2029}]"); }

/* Whether the cursor is at a quantifier in braces ({n}, {n,}, {n,m}); stores its bounds (max -1 for none). */
static bool braces_quantifier(const translation *t, uint32_t *min, int64_t *max, size_t *length) {
  size_t k = t->i + 1;
  uint64_t lo = 0, hi = 0;
  size_t digits = 0;
  for (; k < t->len && t->p[k] >= '0' && t->p[k] <= '9'; k++, digits++) {
    lo = lo < UINT32_MAX ? lo * 10 + (uint64_t)(t->p[k] - '0') : lo;
  }
  if (digits == 0) {
    return false;
  }
  *max = (int64_t)lo;
  if (k < t->len && t->p[k] == ',') {
    k++;
    size_t hi_digits = 0;
    for (; k < t->len && t->p[k] >= '0' && t->p[k] <= '9'; k++, hi_digits++) {
      hi = hi < UINT32_MAX ? hi * 10 + (uint64_t)(t->p[k] - '0') : hi;
    }
    *max = hi_digits == 0 ? -1 : (int64_t)hi;
  }
  if (k >= t->len || t->p[k] != '}') {
    return false;
  }
  *min = lo > UINT32_MAX ? UINT32_MAX : (uint32_t)lo;
  *length = k + 1 - t->i;
  return true;
}

/* group_open translates the '(' at the cursor and what follows it up to the group's body. */
static bool group_open(translation *t, group *stack, int *depth) {
  if (*depth == MAX_DEPTH) {
    return fail(t, "Regular expression too large");
  }
  group *g = &stack[(*depth)++];
  *g = (group){GROUP_PLAIN, t->multiline, t->dot_all};
  t->i++;
  if (peek(t, 0) != '?') {
    puts_(&t->out, "(");
    return true;
  }
  char kind = peek(t, 1);
  if (kind == ':' || kind == '=' || kind == '!') {
    t->i += 2;
    g->kind = kind == ':' ? GROUP_PLAIN : GROUP_LOOKAHEAD;
    printf_(&t->out, "(?%c", kind);
    return true;
  }
  if (kind == '<' && (peek(t, 2) == '=' || peek(t, 2) == '!')) {
    printf_(&t->out, "(?<%c", peek(t, 2));
    t->i += 3;
    g->kind = GROUP_LOOKBEHIND;
    return true;
  }
  if (kind == '<') {
    t->i += 2;
    if (group_name(t) == NULL) {
      return fail(t, "Invalid capture group name");
    }
    puts_(&t->out, "("); /* a numbered group; scan_groups kept its name */
    return true;
  }
  /* Modifiers: (?ims-ims:…). Only i reaches PCRE2; m and s change how ^, $ and . are written. */
  t->i++;
  bool adding = true, ignore_case_set = false, ignore_case = false;
  while (!at_end(t) && t->p[t->i] != ':') {
    char flag = t->p[t->i++];
    if (flag == '-' && adding) {
      adding = false;
    } else if (flag == 'i') {
      ignore_case_set = true;
      ignore_case = adding;
    } else if (flag == 'm') {
      t->multiline = adding;
    } else if (flag == 's') {
      t->dot_all = adding;
    } else {
      return fail(t, "Invalid group");
    }
  }
  if (at_end(t)) {
    return fail(t, "Invalid group");
  }
  t->i++;
  puts_(&t->out, !ignore_case_set ? "(?:" : ignore_case ? "(?i:" : "(?-i:");
  return true;
}

/* translate writes the PCRE2 form of the whole pattern into t->out, or sets t->error. */
static bool translate(translation *t) {
  group stack[MAX_DEPTH];
  int depth = 0;
  bool quantifiable = false;
  while (!at_end(t)) {
    char c = t->p[t->i];
    switch (c) {
    case '\\': {
      atom a;
      if (!escape(t, false, &a)) {
        return false;
      }
      quantifiable = a.kind != ATOM_ASSERTION;
      if (a.unmatchable) {
        puts_(&t->out, "(?!)");
      }
      break;
    }
    case '[':
      if (!character_class(t)) {
        return false;
      }
      quantifiable = true;
      break;
    case '(':
      if (!group_open(t, stack, &depth)) {
        return false;
      }
      quantifiable = false;
      break;
    case ')': {
      if (depth == 0) {
        return fail(t, "Unmatched ')'");
      }
      group *g = &stack[--depth];
      t->multiline = g->multiline;
      t->dot_all = g->dot_all;
      t->i++;
      puts_(&t->out, ")");
      quantifiable = g->kind == GROUP_PLAIN || (g->kind == GROUP_LOOKAHEAD && !t->unicode);
      break;
    }
    case '|':
      t->i++;
      puts_(&t->out, "|");
      quantifiable = false;
      break;
    case '*':
    case '+':
    case '?':
    case '{': {
      uint32_t min = 0;
      int64_t max = 0;
      size_t length = 1;
      if (c == '{' && !braces_quantifier(t, &min, &max, &length)) {
        if (t->unicode) {
          return fail(t, "Lone quantifier brackets");
        }
        t->i++;
        puts_(&t->out, "\\{");
        quantifiable = true;
        break;
      }
      if (!quantifiable) {
        return fail(t, "Nothing to repeat");
      }
      if (c == '{' && max >= 0 && max < (int64_t)min) {
        return fail(t, "numbers out of order in {} quantifier");
      }
      put(&t->out, t->p + t->i, length);
      t->i += length;
      if (peek(t, 0) == '?') {
        t->i++;
        puts_(&t->out, "?");
      }
      quantifiable = false;
      break;
    }
    case '}':
    case ']':
      if (t->unicode) {
        return fail(t, "Lone quantifier brackets");
      }
      t->i++;
      printf_(&t->out, "\\%c", c);
      quantifiable = true;
      break;
    case '.':
      t->i++;
      dot(t);
      quantifiable = true;
      break;
    case '^':
      t->i++;
      line_start(t);
      quantifiable = false;
      break;
    case '$':
      t->i++;
      line_end(t);
      quantifiable = false;
      break;
    default: {
      atom a;
      literal(t, false, &a);
      if (a.unmatchable) {
        puts_(&t->out, "(?!)");
      }
      quantifiable = true;
    }
    }
  }
  if (depth > 0) {
    return fail(t, "Unterminated group");
  }
  return true;
}

/* ---------------------------------------------------------------- compiling */

static unsigned parse_flags(const pts_string *flags) {
  static const char letters[] = "dgimsuy";
  unsigned bits = 0;
  for (uint32_t k = 0; k < flags->byte_len; k++) {
    const char *at = memchr(letters, flags->bytes[k], sizeof letters - 1);
    unsigned bit = at == NULL ? 0 : 1u << (at - letters);
    if (bit == 0 || (bits & bit) != 0) {
      pts_throw_new(&pts_type_builtin_SyntaxError, "Invalid flags supplied to RegExp constructor '%.*s'", (int)flags->byte_len, flags->bytes);
    }
    bits |= bit;
  }
  return bits;
}

/* V8's message shows the pattern as written, not escaped as `source` is. */
_Noreturn static void syntax_error(const pts_string *pattern, const pts_string *flags, const char *message) {
  pts_throw_new(&pts_type_builtin_SyntaxError, "Invalid regular expression: /%.*s/%.*s: %s", (int)pattern->byte_len, pattern->bytes,
                (int)flags->byte_len, flags->bytes, message);
}

pts_regex *pts_regex_compile(const pts_string *pattern, const pts_string *flags) {
  unsigned bits = parse_flags(flags);
  translation t = {
      .p = pattern->bytes,
      .len = pattern->byte_len,
      .unicode = (bits & PTS_REGEX_UNICODE) != 0,
      .multiline = (bits & PTS_REGEX_MULTILINE) != 0,
      .dot_all = (bits & PTS_REGEX_DOT_ALL) != 0,
  };
  if (!scan_groups(&t) || !translate(&t)) {
    free(t.out.bytes);
    syntax_error(pattern, flags, t.error);
  }
  pcre2_compile_context *context = pcre2_compile_context_create(general_context());
  uint32_t extra = PCRE2_EXTRA_ASCII_BSD | PCRE2_EXTRA_ASCII_BSW | (t.unicode ? 0 : PCRE2_EXTRA_CASELESS_RESTRICT);
  pcre2_set_compile_extra_options(context, extra);
  uint32_t options = PCRE2_UTF | PCRE2_UCP | PCRE2_MATCH_INVALID_UTF | PCRE2_ALLOW_EMPTY_CLASS | PCRE2_MATCH_UNSET_BACKREF;
  if ((bits & PTS_REGEX_IGNORE_CASE) != 0) {
    options |= PCRE2_CASELESS;
  }
  int code;
  PCRE2_SIZE offset;
  pcre2_code *compiled = pcre2_compile((PCRE2_SPTR)(t.out.bytes != NULL ? t.out.bytes : ""), t.out.len, options, &code, &offset, context);
  pcre2_compile_context_free(context);
  free(t.out.bytes);
  if (compiled == NULL) {
    PCRE2_UCHAR message[256];
    pcre2_get_error_message(code, message, sizeof message);
    syntax_error(pattern, flags, (const char *)message);
  }
  pts_regex *regex = GC_MALLOC(sizeof *regex);
  if (regex == NULL) {
    pts_panic("RangeError", "Out of memory compiling a regular expression");
  }
  *regex = (pts_regex){compiled, t.names, t.groups, bits};
  return regex;
}

unsigned pts_regex_flags(const pts_regex *regex) { return regex->flags; }

uint32_t pts_regex_group_count(const pts_regex *regex) { return regex->groups; }

pts_string *pts_regex_group_name(const pts_regex *regex, uint32_t group) { return group >= 1 && group <= regex->groups ? regex->names[group] : NULL; }

pts_string *pts_regex_escape_source(const pts_string *pattern) {
  if (pattern->byte_len == 0) {
    return pts_string_from_cstr("(?:)");
  }
  buffer b = {0};
  bool in_class = false;
  for (uint32_t k = 0; k < pattern->byte_len; k++) {
    char c = pattern->bytes[k];
    if (c == '\\' && k + 1 < pattern->byte_len) {
      put(&b, pattern->bytes + k, 2);
      k++;
      continue;
    }
    in_class = c == '[' ? true : c == ']' ? false : in_class;
    if (c == '/' && !in_class) {
      puts_(&b, "\\/");
    } else if (c == '\n') {
      puts_(&b, "\\n");
    } else if (c == '\r') {
      puts_(&b, "\\r");
    } else if ((unsigned char)c == 0xE2 && k + 2 < pattern->byte_len && (unsigned char)pattern->bytes[k + 1] == 0x80 &&
               ((unsigned char)pattern->bytes[k + 2] == 0xA8 || (unsigned char)pattern->bytes[k + 2] == 0xA9)) {
      puts_(&b, (unsigned char)pattern->bytes[k + 2] == 0xA8 ? "\\u2028" : "\\u2029");
      k += 2;
    } else {
      put(&b, &c, 1);
    }
  }
  pts_string *source = pts_string_from_utf8(b.bytes, b.len);
  free(b.bytes);
  return source;
}

/* ---------------------------------------------------------------- matching */

static size_t lead_bytes(unsigned char lead) { return lead < 0x80 ? 1 : lead < 0xE0 ? 2 : lead < 0xF0 ? 3 : 4; }

/* Moves *at to UTF-16 index unit of s (to the next character's start if unit is inside a surrogate pair). */
static void seek_unit(const pts_string *s, pts_regex_cursor *at, uint32_t unit) {
  if ((s->flags & PTS_STRING_ASCII) != 0) {
    at->unit = at->byte = unit;
    return;
  }
  if (unit < at->unit) {
    *at = (pts_regex_cursor){0, 0};
  }
  while (at->unit < unit && at->byte < s->byte_len) {
    unsigned char lead = (unsigned char)s->bytes[at->byte];
    at->byte += (uint32_t)lead_bytes(lead);
    at->unit += lead >= 0xF0 ? 2 : 1;
  }
}

/* Moves *at to byte offset byte of s, a character boundary. */
static void seek_byte(const pts_string *s, pts_regex_cursor *at, uint32_t byte) {
  if ((s->flags & PTS_STRING_ASCII) != 0) {
    at->unit = at->byte = byte;
    return;
  }
  if (byte < at->byte) {
    *at = (pts_regex_cursor){0, 0};
  }
  while (at->byte < byte) {
    unsigned char lead = (unsigned char)s->bytes[at->byte];
    at->byte += (uint32_t)lead_bytes(lead);
    at->unit += lead >= 0xF0 ? 2 : 1;
  }
}

bool pts_regex_exec(const pts_regex *regex, const pts_string *subject, uint32_t from, bool anchored, pts_regex_cursor *cursor,
                    int64_t *captures) {
  if (from > subject->utf16_len) {
    return false;
  }
  seek_unit(subject, cursor, from);
  pcre2_match_data *data = pcre2_match_data_create_from_pattern(regex->code, general_context());
  int found = pcre2_match(regex->code, (PCRE2_SPTR)subject->bytes, subject->byte_len, cursor->byte, anchored ? PCRE2_ANCHORED : 0, data, NULL);
  if (found == PCRE2_ERROR_NOMATCH) {
    pcre2_match_data_free(data);
    return false;
  }
  if (found < 0) {
    pcre2_match_data_free(data);
    pts_throw_new(&pts_type_builtin_RangeError, "Maximum call stack size exceeded");
  }
  PCRE2_SIZE *offsets = pcre2_get_ovector_pointer(data);
  pts_regex_cursor start = *cursor;
  for (uint32_t g = 0; g <= regex->groups; g++) {
    if ((int)g >= found || offsets[2 * g] == PCRE2_UNSET) {
      captures[2 * g] = captures[2 * g + 1] = -1;
      continue;
    }
    pts_regex_cursor at = start;
    seek_byte(subject, &at, (uint32_t)offsets[2 * g]);
    captures[2 * g] = at.unit;
    seek_byte(subject, &at, (uint32_t)offsets[2 * g + 1]);
    captures[2 * g + 1] = at.unit;
    if (g == 0) {
      *cursor = at;
    }
  }
  pcre2_match_data_free(data);
  return true;
}
