/* What inspect.c shares with console.c and table.c: Node's util.inspect and util.format (D-027, D-088). */
#ifndef PTS_INSPECT_INTERNAL_H
#define PTS_INSPECT_INTERNAL_H

#include "pts.h"

#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

/* A growable, NUL-terminated byte string on the C heap (inspect output holds no GC pointers). */
typedef struct text {
  char *bytes;
  size_t len, cap;
} text;

static inline void text_add(text *t, const char *bytes, size_t len) {
  if (t->len + len + 1 > t->cap) {
    t->cap = (t->len + len + 1) * 2;
    t->bytes = realloc(t->bytes, t->cap);
    if (t->bytes == NULL) {
      pts_panic("RangeError", "Out of memory formatting a value for console.log");
    }
  }
  if (len > 0) { /* an empty text's bytes may be NULL */
    memcpy(t->bytes + t->len, bytes, len);
  }
  t->len += len;
  t->bytes[t->len] = '\0';
}

static inline void text_puts(text *t, const char *cstr) { text_add(t, cstr, strlen(cstr)); }

static inline void text_printf(text *t, const char *format, ...) __attribute__((format(printf, 2, 3)));

static inline void text_printf(text *t, const char *format, ...) {
  char small[64];
  va_list args;
  va_start(args, format);
  int len = vsnprintf(small, sizeof small, format, args);
  va_end(args);
  text_add(t, small, (size_t)len); /* callers format short numbers and references */
}

/* console.log's arguments as Node formats them, in a NUL-terminated buffer on the C heap that the caller frees. */
char *pts_format(size_t argc, const pts_value *argv, size_t *len);
/* util.inspect(value, { depth, showHidden }) (depth INFINITY for null), as pts_format returns it. */
char *pts_inspect(pts_value value, double depth, bool show_hidden, size_t *len);
/* console.table's inspection of a cell: one line, three elements of an array, an object of more than two keys as [Name]. */
void pts_inspect_cell(text *out, pts_value value);
/* Node's getStringWidth: the columns text takes in a terminal. */
size_t pts_text_width(const char *bytes, size_t len);
/* console.table(data, properties) as Node's cli_table draws it (table.c), as pts_format returns it. */
char *pts_table(pts_value data, pts_value properties, size_t *len);

#endif
