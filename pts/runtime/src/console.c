#include "pts.h"

#include <inttypes.h>
#include <math.h>
#include <stdio.h>

void pts_inspect_write(FILE *out, const pts_object *object); /* inspect.c */

/*
 * Node's util.inspect for a top-level argument: strings print raw, -0 prints
 * as "-0", other primitives as String(x), and class instances as inspect.c
 * formats them. The full inspect port is M3.6 (D-O16).
 */
static void write_value(FILE *out, pts_value value) {
  if (pts_value_is_number(value)) {
    double number = pts_value_as_number(value);
    if (number == 0 && signbit(number)) {
      fputs("-0", out);
      return;
    }
    char buf[PTS_NUMBER_BUF_SIZE];
    size_t len = pts_number_format(number, buf);
    fwrite(buf, 1, len, out);
    return;
  }
  if (pts_value_is_object(value)) {
    pts_object *object = pts_value_as_object(value);
    switch (object->type->kind) {
    case PTS_KIND_STRING:
      pts_string_write_for_output((pts_string *)object, out);
      return;
    case PTS_KIND_INT64:
      fprintf(out, "%" PRId64, ((pts_boxed_int64 *)object)->value);
      return;
    case PTS_KIND_UINT64:
      fprintf(out, "%" PRIu64, ((pts_boxed_uint64 *)object)->value);
      return;
    case PTS_KIND_OBJECT:
    case PTS_KIND_ARRAY:
    case PTS_KIND_TUPLE:
    case PTS_KIND_FUNCTION:
    case PTS_KIND_MAP:
    case PTS_KIND_SET:
    case PTS_KIND_RECORD:
    case PTS_KIND_DATE:
      pts_inspect_write(out, object);
      return;
    }
    pts_panic("Error", "console.log of a %s value is not supported yet", object->type->name);
  }
  switch (value) {
  case PTS_VALUE_UNDEFINED:
    fputs("undefined", out);
    return;
  case PTS_VALUE_NULL:
    fputs("null", out);
    return;
  case PTS_VALUE_TRUE:
    fputs("true", out);
    return;
  case PTS_VALUE_FALSE:
    fputs("false", out);
    return;
  default:
    pts_panic("Error", "console.log got a malformed value 0x%llx", (unsigned long long)value);
  }
}

void pts_console_log(size_t argc, const pts_value *argv) {
  for (size_t i = 0; i < argc; i++) {
    if (i > 0) {
      fputc(' ', stdout);
    }
    write_value(stdout, argv[i]);
  }
  fputc('\n', stdout);
}

static void write_argument(bool *first, pts_value value) {
  if (!*first) {
    fputc(' ', stdout);
  }
  *first = false;
  write_value(stdout, value);
}

void pts_console_log_spread(size_t argc, const pts_value *argv, const bool *spread) {
  bool first = true;
  for (size_t i = 0; i < argc; i++) {
    if (!spread[i]) {
      write_argument(&first, argv[i]);
      continue;
    }
    const pts_array *array = (const pts_array *)pts_value_as_object(argv[i]);
    for (size_t j = 0; j < pts_array_length_of(array); j++) { // the length now, as JS's array iterator reads it
      write_argument(&first, pts_array_element(array, (double)j));
    }
  }
  fputc('\n', stdout);
}
