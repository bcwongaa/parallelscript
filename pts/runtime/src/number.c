#include "pts.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>

/* The longest Number::toString output is 25 bytes, e.g. "-1.2345678901234567e-308". */
_Static_assert(PTS_NUMBER_BUF_SIZE >= 26, "number buffer too small");

enum { MAX_SIGNIFICANT_DIGITS = 17 };

/*
 * Finds the shortest decimal digit string that round-trips to `value` (> 0,
 * finite). Writes the digits (no dot, no leading zeros) and returns their
 * count; *point is the position of the decimal point relative to the first
 * digit, i.e. value = 0.DIGITS * 10^point.
 *
 * printf's %.*e is correctly rounded, so the first precision that round-trips
 * gives the shortest digits closest to the value, which is what ECMAScript
 * Number::toString requires. M1.1 replaces this with a Ryu-style algorithm.
 */
static int shortest_digits(double value, char digits[MAX_SIGNIFICANT_DIGITS + 1], int *point) {
  char scientific[40];
  for (int precision = 0; precision < MAX_SIGNIFICANT_DIGITS; precision++) {
    snprintf(scientific, sizeof scientific, "%.*e", precision, value);
    if (strtod(scientific, NULL) == value || precision == MAX_SIGNIFICANT_DIGITS - 1) {
      break;
    }
  }
  /* scientific is "D.DDDDe[+-]XX" or "De[+-]XX". */
  int count = 0;
  const char *cursor = scientific;
  for (; *cursor != 'e'; cursor++) {
    if (*cursor != '.') {
      digits[count++] = *cursor;
    }
  }
  while (count > 1 && digits[count - 1] == '0') {
    count--;
  }
  digits[count] = '\0';
  *point = atoi(cursor + 1) + 1;
  return count;
}

size_t pts_number_format(double value, char *buf) {
  if (isnan(value)) {
    return (size_t)sprintf(buf, "NaN");
  }
  if (isinf(value)) {
    return (size_t)sprintf(buf, value > 0 ? "Infinity" : "-Infinity");
  }
  if (value == 0) {
    return (size_t)sprintf(buf, "0"); /* both +0 and -0 */
  }

  char *out = buf;
  if (value < 0) {
    *out++ = '-';
    value = -value;
  }
  char digits[MAX_SIGNIFICANT_DIGITS + 1];
  int point;
  int count = shortest_digits(value, digits, &point);

  if (count <= point && point <= 21) {
    /* Integer: digits then zeros. */
    memcpy(out, digits, (size_t)count);
    out += count;
    for (int i = count; i < point; i++) {
      *out++ = '0';
    }
  } else if (0 < point && point <= 21) {
    /* Fixed with the point inside the digits. */
    memcpy(out, digits, (size_t)point);
    out += point;
    *out++ = '.';
    memcpy(out, digits + point, (size_t)(count - point));
    out += count - point;
  } else if (-6 < point && point <= 0) {
    /* Small fixed: 0.000ddd */
    *out++ = '0';
    *out++ = '.';
    for (int i = point; i < 0; i++) {
      *out++ = '0';
    }
    memcpy(out, digits, (size_t)count);
    out += count;
  } else {
    /* Exponential: d[.ddd]e+x */
    *out++ = digits[0];
    if (count > 1) {
      *out++ = '.';
      memcpy(out, digits + 1, (size_t)(count - 1));
      out += count - 1;
    }
    out += sprintf(out, "e%c%d", point - 1 >= 0 ? '+' : '-', abs(point - 1));
  }
  *out = '\0';
  return (size_t)(out - buf);
}

pts_string *pts_number_to_string(double value) {
  char buf[PTS_NUMBER_BUF_SIZE];
  size_t len = pts_number_format(value, buf);
  return pts_string_from_utf8(buf, len);
}
