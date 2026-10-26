/*
 * Number.prototype's formatting methods and the global parseInt and parseFloat
 * (D-083). toFixed, toExponential and toPrecision round a number's exact decimal value
 * half up, as ECMAScript says ("pick the larger n"); glibc's printf gives the exact
 * digits when its precision covers every fractional digit a double can have.
 * toString(radix) is V8's DoubleToRadixCString, and toLocaleString formats the shortest
 * digits as Node's default (English) NumberFormat does.
 */
#include "string_internal.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>

#include "ryu/ryu.h"

enum {
  MAX_FRACTION_DIGITS = 100, /* the most toFixed and toExponential take */
  MAX_PRECISION = 100,
  /* A double's exact decimal expansion: at most 309 integer digits and 1074 fractional ones. */
  EXACT_BUF_SIZE = 309 + 1 + 1100 + 8,
};

/* ECMAScript ToIntegerOrInfinity. */
static double to_integer(double d) { return d != d ? 0 : __builtin_trunc(d) + 0.0; }

static pts_string *string_of(const char *bytes, size_t len) { return pts_string_from_utf8(bytes, len); }

/* ---------------------------------------------------------------- exact digits */

/*
 * The exact decimal digits of x (finite, > 0), without leading or trailing zeros, into
 * digits (at least EXACT_BUF_SIZE bytes); returns their count and sets *point so that
 * x = 0.DIGITS * 10^point.
 */
static int exact_digits(double x, char *digits, int *point) {
  int binary_exponent;
  frexp(x, &binary_exponent);
  int fraction_digits = 53 - binary_exponent; /* x is a multiple of 2^-(53 - e), whose expansion ends there */
  char text[EXACT_BUF_SIZE];
  snprintf(text, sizeof text, "%.*f", fraction_digits > 0 ? fraction_digits : 0, x);
  const char *dot = strchr(text, '.');
  int integer_digits = dot == NULL ? (int)strlen(text) : (int)(dot - text);
  int count = 0;
  *point = integer_digits;
  for (const char *c = text; *c != '\0'; c++) {
    if (*c == '.') {
      continue;
    }
    if (count == 0 && *c == '0') {
      (*point)--; /* a leading zero, before or after the point */
      continue;
    }
    digits[count++] = *c;
  }
  while (count > 0 && digits[count - 1] == '0') {
    count--;
  }
  digits[count] = '\0';
  return count;
}

/*
 * Rounds the number 0.DIGITS * 10^point (count digits) half up to its first keep digits
 * (keep >= 0), padding with zeros. A carry out of the first digit leaves "1" and zeros
 * and increments *point. Returns the number of digits held: keep, or 1 if keep is 0 and
 * the number rounded up to 10^point.
 */
static int round_half_up(char *digits, int count, int keep, int *point) {
  bool up = keep < count && digits[keep] >= '5';
  for (int i = count; i < keep; i++) {
    digits[i] = '0';
  }
  digits[keep] = '\0';
  if (!up) {
    return keep;
  }
  int i = keep - 1;
  while (i >= 0 && digits[i] == '9') {
    digits[i--] = '0';
  }
  if (i >= 0) {
    digits[i]++;
    return keep;
  }
  digits[0] = '1';
  (*point)++;
  if (keep == 0) {
    digits[1] = '\0';
    return 1;
  }
  return keep;
}

/* The digit at position i of 0.DIGITS (count digits): '0' outside them. */
static char digit_at(const char *digits, int count, int i) { return i >= 0 && i < count ? digits[i] : '0'; }

/* ---------------------------------------------------------------- toFixed, toExponential, toPrecision */

static void throw_digits_range(const char *method, int low, int high) {
  pts_throw_new(&pts_type_builtin_RangeError, "%s() digits argument must be between %d and %d", method, low, high);
}

pts_string *pts_number_to_fixed(double x, double fraction_digits) {
  double f = to_integer(fraction_digits);
  if (!(f >= 0 && f <= MAX_FRACTION_DIGITS)) {
    throw_digits_range("toFixed", 0, MAX_FRACTION_DIGITS);
  }
  if (!isfinite(x) || fabs(x) >= 1e21) {
    return pts_number_to_string(x);
  }
  char out[EXACT_BUF_SIZE + 4], *o = out, digits[EXACT_BUF_SIZE];
  if (x < 0) {
    *o++ = '-';
    x = -x;
  }
  int fraction = (int)f, point = 0, count = 0;
  if (x > 0) {
    count = exact_digits(x, digits, &point);
    int keep = point + fraction; /* the digits down to the f-th after the point */
    count = keep < 0 ? 0 : round_half_up(digits, count, keep, &point);
  }
  if (point <= 0) {
    *o++ = '0';
  }
  for (int i = 0; i < point; i++) {
    *o++ = digit_at(digits, count, i);
  }
  if (fraction > 0) {
    *o++ = '.';
    for (int i = point; i < point + fraction; i++) {
      *o++ = digit_at(digits, count, i);
    }
  }
  return string_of(out, (size_t)(o - out));
}

/* Writes "e+X" or "e-X". */
static char *put_exponent(char *o, int e) {
  return o + sprintf(o, "e%c%d", e < 0 ? '-' : '+', abs(e));
}

/* x (finite, nonzero) to `significant` digits rounded half up: the digits and the decimal exponent e of the first. */
static int significant_digits(double x, int significant, char *digits, int *e) {
  int point;
  int count = exact_digits(x, digits, &point);
  round_half_up(digits, count, significant, &point);
  *e = point - 1;
  return significant;
}

pts_string *pts_number_to_exponential(double x, double fraction_digits) {
  double f = to_integer(fraction_digits);
  if (!isfinite(x)) {
    return pts_number_to_string(x);
  }
  if (!(f >= 0 && f <= MAX_FRACTION_DIGITS)) {
    pts_throw_new(&pts_type_builtin_RangeError, "toExponential() argument must be between 0 and %d", MAX_FRACTION_DIGITS);
  }
  char out[EXACT_BUF_SIZE + 16], *o = out, digits[EXACT_BUF_SIZE];
  if (x < 0) {
    *o++ = '-';
    x = -x;
  }
  int significant = (int)f + 1, e = 0;
  if (x == 0) {
    memset(digits, '0', (size_t)significant);
  } else {
    significant_digits(x, significant, digits, &e);
  }
  *o++ = digits[0];
  if (significant > 1) {
    *o++ = '.';
    memcpy(o, digits + 1, (size_t)significant - 1);
    o += significant - 1;
  }
  o = put_exponent(o, e);
  return string_of(out, (size_t)(o - out));
}

/* toExponential() without an argument: as many digits as the number needs (Number::toString's). */
pts_string *pts_number_to_exponential_shortest(double x) {
  if (!isfinite(x)) {
    return pts_number_to_string(x);
  }
  char out[64], *o = out;
  if (x < 0) {
    *o++ = '-';
    x = -x;
  }
  if (x == 0) {
    return string_of("0e+0", 4);
  }
  char scientific[32];
  int len = d2s_buffered_n(x, scientific); /* "D[.DDD]E[-]X" */
  scientific[len] = '\0';
  char *exponent = strchr(scientific, 'E');
  memcpy(o, scientific, (size_t)(exponent - scientific));
  o += exponent - scientific;
  o = put_exponent(o, atoi(exponent + 1));
  return string_of(out, (size_t)(o - out));
}

pts_string *pts_number_to_precision(double x, double precision) {
  double p = to_integer(precision);
  if (!isfinite(x)) {
    return pts_number_to_string(x);
  }
  if (!(p >= 1 && p <= MAX_PRECISION)) {
    pts_throw_new(&pts_type_builtin_RangeError, "toPrecision() argument must be between 1 and %d", MAX_PRECISION);
  }
  char out[EXACT_BUF_SIZE + 16], *o = out, digits[EXACT_BUF_SIZE];
  if (x < 0) {
    *o++ = '-';
    x = -x;
  }
  int significant = (int)p, e = 0;
  if (x == 0) {
    memset(digits, '0', (size_t)significant);
  } else {
    significant_digits(x, significant, digits, &e);
  }
  if (x != 0 && (e < -6 || e >= significant)) {
    *o++ = digits[0];
    if (significant > 1) {
      *o++ = '.';
      memcpy(o, digits + 1, (size_t)significant - 1);
      o += significant - 1;
    }
    o = put_exponent(o, e);
  } else if (e >= 0) {
    memcpy(o, digits, (size_t)e + 1);
    o += e + 1;
    if (e + 1 < significant) {
      *o++ = '.';
      memcpy(o, digits + e + 1, (size_t)(significant - e - 1));
      o += significant - e - 1;
    }
  } else {
    *o++ = '0';
    *o++ = '.';
    for (int i = 0; i < -(e + 1); i++) {
      *o++ = '0';
    }
    memcpy(o, digits, (size_t)significant);
    o += significant;
  }
  return string_of(out, (size_t)(o - out));
}

/* ---------------------------------------------------------------- toString(radix) */

static const char radix_chars[] = "0123456789abcdefghijklmnopqrstuvwxyz";

/* The exponent of a double's 53-bit integer significand (V8's Double::Exponent()). */
static int significand_exponent(double d) {
  uint64_t bits;
  memcpy(&bits, &d, sizeof bits);
  int biased = (int)((bits >> 52) & 0x7FF);
  return biased == 0 ? -1074 : biased - 0x3FF - 52;
}

/* V8's DoubleToRadixCString: the integer digits exactly, the fraction digits until they pin the double down. */
static pts_string *radix_string(double value, int radix) {
  enum { BUFFER_SIZE = 2200 };
  char buffer[BUFFER_SIZE];
  int integer_cursor = BUFFER_SIZE / 2, fraction_cursor = integer_cursor;
  bool negative = value < 0;
  if (negative) {
    value = -value;
  }
  double integer = floor(value), fraction = value - integer;
  /* Only as many fraction digits as the input's precision gives. */
  double delta = 0.5 * (nextafter(value, INFINITY) - value);
  delta = fmax(nextafter(0.0, 1.0), delta);
  if (fraction >= delta) {
    buffer[fraction_cursor++] = '.';
    do {
      fraction *= radix;
      delta *= radix;
      int digit = (int)fraction;
      buffer[fraction_cursor++] = radix_chars[digit];
      fraction -= digit;
      if (fraction > 0.5 || (fraction == 0.5 && (digit & 1))) { /* round to even */
        if (fraction + delta > 1) {
          /* Carry back through the digits written. */
          for (;;) {
            fraction_cursor--;
            if (fraction_cursor == BUFFER_SIZE / 2) {
              integer += 1; /* into the integer part */
              break;
            }
            char c = buffer[fraction_cursor];
            int written = c > '9' ? c - 'a' + 10 : c - '0';
            if (written + 1 < radix) {
              buffer[fraction_cursor++] = radix_chars[written + 1];
              break;
            }
          }
          break;
        }
      }
    } while (fraction >= delta);
  }
  /* Integer digits; the ones below the double's precision are zeros. */
  while (significand_exponent(integer / radix) > 0) {
    integer /= radix;
    buffer[--integer_cursor] = '0';
  }
  do {
    double remainder = fmod(integer, radix);
    buffer[--integer_cursor] = radix_chars[(int)remainder];
    integer = (integer - remainder) / radix;
  } while (integer > 0);
  if (negative) {
    buffer[--integer_cursor] = '-';
  }
  return string_of(buffer + integer_cursor, (size_t)(fraction_cursor - integer_cursor));
}

pts_string *pts_number_to_string_radix(double x, double radix) {
  double r = to_integer(radix);
  if (!(r >= 2 && r <= 36)) {
    pts_throw_new(&pts_type_builtin_RangeError, "toString() radix argument must be between 2 and 36");
  }
  if (r == 10 || !isfinite(x)) {
    return pts_number_to_string(x);
  }
  return radix_string(x, (int)r);
}

/* ---------------------------------------------------------------- toLocaleString */

enum { LOCALE_FRACTION_DIGITS = 3 };

/* Node's default (English) NumberFormat: the shortest digits rounded half up to 3 decimals, grouped by commas. */
pts_string *pts_number_to_locale_string(double x) {
  if (x != x) {
    return string_of("NaN", 3);
  }
  char out[512], *o = out;
  if (signbit(x)) {
    *o++ = '-';
    x = -x;
  }
  if (isinf(x)) {
    o += pts_utf8_encode(0x221E, o); /* ∞ */
    return string_of(out, (size_t)(o - out));
  }
  char digits[64];
  int point = 0, count = 0;
  if (x != 0) {
    char scientific[32];
    int len = d2s_buffered_n(x, scientific); /* "D[.DDD]E[-]X" */
    scientific[len] = '\0';
    for (const char *c = scientific; *c != 'E'; c++) {
      if (*c != '.') {
        digits[count++] = *c;
      }
    }
    point = atoi(strchr(scientific, 'E') + 1) + 1;
    int keep = point + LOCALE_FRACTION_DIGITS;
    if (keep < count) {
      count = keep < 0 ? 0 : round_half_up(digits, count, keep, &point);
    }
  }
  if (point <= 0) {
    *o++ = '0';
  }
  for (int i = 0; i < point; i++) {
    if (i > 0 && (point - i) % 3 == 0) {
      *o++ = ',';
    }
    *o++ = digit_at(digits, count, i);
  }
  int last = count; /* fraction digits without trailing zeros */
  while (last > 0 && last > point && digits[last - 1] == '0') {
    last--;
  }
  if (last > 0 && last > point) {
    *o++ = '.';
    for (int i = point; i < last; i++) {
      *o++ = digit_at(digits, count, i);
    }
  }
  return string_of(out, (size_t)(o - out));
}

/* ---------------------------------------------------------------- parseInt and parseFloat */

/* The value of digit character c in radix, or -1. */
static int digit_value(unsigned char c, int radix) {
  int d = c >= '0' && c <= '9' ? c - '0' : (c | 0x20) >= 'a' && (c | 0x20) <= 'z' ? (c | 0x20) - 'a' + 10 : 99;
  return d < radix ? d : -1;
}

/* The digits s[0..n) in a power-of-two radix, rounded to the nearest double (ties to even), as V8 does. */
static double power_of_two_digits(const char *s, size_t n, int radix) {
  int bits_per_digit = __builtin_ctz((unsigned)radix);
  size_t i = 0;
  while (i < n && s[i] == '0') {
    i++;
  }
  int64_t number = 0;
  int exponent = 0;
  for (; i < n; i++) {
    number = number * radix + digit_value((unsigned char)s[i], radix);
    int overflow = (int)(number >> 53);
    if (overflow == 0) {
      continue;
    }
    int overflow_bits = 1;
    while (overflow > 1) {
      overflow_bits++;
      overflow >>= 1;
    }
    int dropped_mask = (1 << overflow_bits) - 1, dropped = (int)(number & dropped_mask);
    number >>= overflow_bits;
    exponent = overflow_bits;
    bool zero_tail = true;
    for (i++; i < n; i++) {
      zero_tail = zero_tail && s[i] == '0';
      exponent += bits_per_digit;
    }
    int middle = 1 << (overflow_bits - 1);
    if (dropped > middle || (dropped == middle && ((number & 1) != 0 || !zero_tail))) {
      number++;
    }
    if (number & ((int64_t)1 << 53)) {
      exponent++;
      number >>= 1;
    }
    break;
  }
  return ldexp((double)number, exponent);
}

/* The digits in another radix, accumulated in 32-bit parts as V8 does (approximate beyond 2^53, as the spec allows). */
static double other_radix_digits(const char *s, size_t n, int radix) {
#if defined(__aarch64__)
#pragma STDC FP_CONTRACT ON /* `result * multiplier + part` is one FMA in Node's arm64 builds (ieee754.c, C362) */
#endif
  const uint32_t max_multiplier = 0xFFFFFFFFu / 36;
  double result = 0;
  size_t i = 0;
  while (i < n) {
    uint32_t part = 0, multiplier = 1;
    while (i < n) {
      uint32_t m = multiplier * (uint32_t)radix;
      if (m > max_multiplier) {
        break;
      }
      part = part * (uint32_t)radix + (uint32_t)digit_value((unsigned char)s[i], radix);
      multiplier = m;
      i++;
    }
    result = result * multiplier + part;
  }
  return result;
}

static bool is_js_space(unsigned code_point) {
  switch (code_point) {
  case 0x09: case 0x0A: case 0x0B: case 0x0C: case 0x0D: case 0x20: case 0xA0: case 0x1680:
  case 0x2028: case 0x2029: case 0x202F: case 0x205F: case 0x3000: case 0xFEFF:
    return true;
  default:
    return code_point >= 0x2000 && code_point <= 0x200A;
  }
}

/* The byte offset of the first character that isn't JS whitespace. */
static uint32_t skip_space(const pts_string *string) {
  uint32_t pos = 0;
  while (pos < string->byte_len) {
    unsigned length, code_point = pts_utf8_decode(string->bytes + pos, &length);
    if (!is_js_space(code_point)) {
      break;
    }
    pos += length;
  }
  return pos;
}

double pts_parse_int(pts_string *string, double radix) {
  const char *s = string->bytes + skip_space(string);
  size_t n = string->byte_len - (size_t)(s - string->bytes);
  double sign = 1;
  if (n > 0 && (s[0] == '-' || s[0] == '+')) {
    sign = s[0] == '-' ? -1 : 1;
    s++, n--;
  }
  int32_t r = pts_number_to_int32(radix);
  bool strip_prefix = true;
  if (r != 0) {
    if (r < 2 || r > 36) {
      return NAN;
    }
    strip_prefix = r == 16;
  } else {
    r = 10;
  }
  if (strip_prefix && n >= 2 && s[0] == '0' && (s[1] | 0x20) == 'x') {
    s += 2, n -= 2;
    r = 16;
  }
  size_t end = 0;
  while (end < n && digit_value((unsigned char)s[end], r) >= 0) {
    end++;
  }
  if (end == 0) {
    return NAN;
  }
  if (r == 10) {
    char *copy = GC_MALLOC_ATOMIC(end + 1);
    memcpy(copy, s, end);
    copy[end] = '\0';
    return sign * strtod(copy, NULL); /* correctly rounded */
  }
  if ((r & (r - 1)) == 0) {
    return sign * power_of_two_digits(s, end, r);
  }
  return sign * other_radix_digits(s, end, r);
}

/* The length of the longest prefix of s that is a StrDecimalLiteral without its sign, or 0. */
static size_t decimal_prefix(const char *s, size_t n) {
  if (n >= 8 && memcmp(s, "Infinity", 8) == 0) {
    return 8;
  }
  size_t i = 0, digits = 0;
  while (i < n && s[i] >= '0' && s[i] <= '9') {
    i++, digits++;
  }
  if (i < n && s[i] == '.') {
    size_t after = i + 1, fraction_digits = 0;
    while (after < n && s[after] >= '0' && s[after] <= '9') {
      after++, fraction_digits++;
    }
    if (digits + fraction_digits > 0) {
      i = after, digits += fraction_digits;
    }
  }
  if (digits == 0) {
    return 0;
  }
  if (i < n && (s[i] | 0x20) == 'e') {
    size_t e = i + 1;
    if (e < n && (s[e] == '+' || s[e] == '-')) {
      e++;
    }
    size_t exponent_digits = 0;
    while (e < n && s[e] >= '0' && s[e] <= '9') {
      e++, exponent_digits++;
    }
    if (exponent_digits > 0) {
      i = e;
    }
  }
  return i;
}

double pts_parse_float(pts_string *string) {
  const char *s = string->bytes + skip_space(string);
  size_t n = string->byte_len - (size_t)(s - string->bytes);
  size_t sign = n > 0 && (s[0] == '-' || s[0] == '+') ? 1 : 0;
  size_t length = decimal_prefix(s + sign, n - sign);
  if (length == 0) {
    return NAN;
  }
  if (s[sign] == 'I') {
    return s[0] == '-' ? -INFINITY : INFINITY;
  }
  char *copy = GC_MALLOC_ATOMIC(sign + length + 1);
  memcpy(copy, s, sign + length);
  copy[sign + length] = '\0';
  return strtod(copy, NULL);
}

/* ---------------------------------------------------------------- 64-bit integers */

static void check_radix(double r) {
  if (!(r >= 2 && r <= 36)) {
    pts_throw_new(&pts_type_builtin_RangeError, "toString() radix argument must be between 2 and 36");
  }
}

static pts_string *magnitude_string(uint64_t magnitude, bool negative, int radix) {
  char buffer[66];
  int cursor = sizeof buffer;
  do {
    buffer[--cursor] = radix_chars[magnitude % (unsigned)radix];
    magnitude /= (unsigned)radix;
  } while (magnitude > 0);
  if (negative) {
    buffer[--cursor] = '-';
  }
  return string_of(buffer + cursor, sizeof buffer - (size_t)cursor);
}

pts_string *pts_i64_to_string_radix(int64_t value, double radix) {
  double r = to_integer(radix);
  check_radix(r);
  return magnitude_string(value < 0 ? -(uint64_t)value : (uint64_t)value, value < 0, (int)r);
}

pts_string *pts_u64_to_string_radix(uint64_t value, double radix) {
  double r = to_integer(radix);
  check_radix(r);
  return magnitude_string(value, false, (int)r);
}

/* ---------------------------------------------------------------- Number's predicates of dynamic values */

/* Whether value is a number (a boxed 64-bit integer too), and which. */
static bool number_of(pts_value value, double *number) {
  if (pts_value_is_number(value)) {
    *number = pts_value_as_number(value);
    return true;
  }
  if (pts_value_is_object(value)) {
    pts_kind kind = pts_value_as_object(value)->type->kind;
    if (kind == PTS_KIND_INT64 || kind == PTS_KIND_UINT64) {
      *number = pts_value_to_number(value);
      return true;
    }
  }
  return false;
}

bool pts_value_is_integer(pts_value value) {
  double x;
  return number_of(value, &x) && pts_number_is_integer(x);
}

bool pts_value_is_safe_integer(pts_value value) {
  double x;
  return number_of(value, &x) && pts_number_is_safe_integer(x);
}

bool pts_value_is_finite(pts_value value) {
  double x;
  return number_of(value, &x) && pts_number_is_finite(x);
}

bool pts_value_is_nan(pts_value value) {
  double x;
  return number_of(value, &x) && pts_number_is_nan(x);
}
