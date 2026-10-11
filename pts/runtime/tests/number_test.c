#include "pts.h"

#include <math.h>

#include "check.h"

static void check_format(double value, const char *expected) {
  char buf[PTS_NUMBER_BUF_SIZE];
  size_t len = pts_number_format(value, buf);
  CHECK(len == strlen(buf));
  CHECK_STR(buf, expected);
}

/* Expected strings are what Node prints for String(x). */
static void formats_like_javascript(void) {
  check_format(0.0, "0");
  check_format(-0.0, "0");
  check_format(NAN, "NaN");
  check_format(INFINITY, "Infinity");
  check_format(-INFINITY, "-Infinity");
  check_format(1, "1");
  check_format(-42, "-42");
  check_format(0.1 + 0.2, "0.30000000000000004");
  check_format(1.5, "1.5");
  check_format(123456789012345680000.0, "123456789012345680000");
  check_format(1e21, "1e+21");
  check_format(1.5e21, "1.5e+21");
  check_format(0.000001, "0.000001");
  check_format(0.0000001, "1e-7");
  check_format(1.2345e-7, "1.2345e-7");
  check_format(5e-324, "5e-324");
  check_format(1.7976931348623157e308, "1.7976931348623157e+308");
  check_format(-1.2345678901234567e-308, "-1.2345678901234567e-308");
  check_format(9007199254740993.0, "9007199254740992");
  check_format(100, "100");
  check_format(1e20, "100000000000000000000");
  check_format(123.456, "123.456");
}

static bool same(double a, double b) { return memcmp(&a, &b, sizeof a) == 0 || (a != a && b != b); }

/* Expected values are what Node computes. */
static void remainder_follows_javascript(void) {
  CHECK(same(pts_number_rem(5.5, 2), 1.5));
  CHECK(same(pts_number_rem(-5.5, 2), -1.5));
  CHECK(same(pts_number_rem(5.5, -2), 1.5));
  CHECK(same(pts_number_rem(-0.0, 3), -0.0));
  CHECK(same(pts_number_rem(4, 2), 0.0));
  CHECK(same(pts_number_rem(-4, 2), -0.0));
  CHECK(same(pts_number_rem(5, INFINITY), 5));
  CHECK(same(pts_number_rem(5, 0), NAN));
  CHECK(same(pts_number_rem(INFINITY, 2), NAN));
  CHECK(same(pts_number_rem(NAN, 2), NAN));
}

static void power_follows_javascript(void) {
  CHECK(same(pts_number_pow(2, 10), 1024));
  CHECK(same(pts_number_pow(1, NAN), NAN));
  CHECK(same(pts_number_pow(1, INFINITY), NAN));
  CHECK(same(pts_number_pow(-1, -INFINITY), NAN));
  CHECK(same(pts_number_pow(NAN, 0), 1));
  CHECK(same(pts_number_pow(0, -1), INFINITY));
  CHECK(same(pts_number_pow(-0.0, -1), -INFINITY));
  CHECK(same(pts_number_pow(-0.0, 3), -0.0));
  CHECK(same(pts_number_pow(-8, 1.0 / 3), NAN));
  CHECK(same(pts_number_pow(0.5, INFINITY), 0));
  CHECK(same(pts_number_pow(2, -INFINITY), 0));
}

/* Reads one hex bit pattern per line and prints Number::toString, for the Go differential test. */
static int format_stdin(void) {
  char line[64];
  while (fgets(line, sizeof line, stdin) != NULL) {
    unsigned long long bits = strtoull(line, NULL, 16);
    double value;
    memcpy(&value, &bits, sizeof value);
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(value, buf);
    puts(buf);
  }
  return 0;
}

/* Expected strings are what Node gives. */
static void to_fixed_rounds_the_exact_value_half_up(void) {
  CHECK_STR(pts_number_to_fixed(3.14159, 2)->bytes, "3.14");
  CHECK_STR(pts_number_to_fixed(0.5, 0)->bytes, "1");
  CHECK_STR(pts_number_to_fixed(2.5, 0)->bytes, "3");
  CHECK_STR(pts_number_to_fixed(1.005, 2)->bytes, "1.00"); /* 1.00499999999999989... */
  CHECK_STR(pts_number_to_fixed(1.25, 1)->bytes, "1.3");
  CHECK_STR(pts_number_to_fixed(999.95, 1)->bytes, "1000.0"); /* 999.950000000000045... */
  CHECK_STR(pts_number_to_fixed(999.96, 1)->bytes, "1000.0");
  CHECK_STR(pts_number_to_fixed(-1.5, 0)->bytes, "-2");
  CHECK_STR(pts_number_to_fixed(-0.0001, 2)->bytes, "-0.00");
  CHECK_STR(pts_number_to_fixed(-0.0, 2)->bytes, "0.00");
  CHECK_STR(pts_number_to_fixed(0.006, 2)->bytes, "0.01");
  CHECK_STR(pts_number_to_fixed(0.004, 2)->bytes, "0.00");
  CHECK_STR(pts_number_to_fixed(42, NAN)->bytes, "42");
  CHECK_STR(pts_number_to_fixed(1e21, 2)->bytes, "1e+21");
  CHECK_STR(pts_number_to_fixed(1e-10, 3)->bytes, "0.000");
  CHECK_STR(pts_number_to_fixed(123.456, 10)->bytes, "123.4560000000");
  CHECK_STR(pts_number_to_fixed(NAN, 2)->bytes, "NaN");
}

static void exponential_and_precision_round_half_up(void) {
  CHECK_STR(pts_number_to_exponential(123456, 2)->bytes, "1.23e+5");
  CHECK_STR(pts_number_to_exponential(0, 2)->bytes, "0.00e+0");
  CHECK_STR(pts_number_to_exponential(9.99, 1)->bytes, "1.0e+1");
  CHECK_STR(pts_number_to_exponential(-0.00015, 0)->bytes, "-1e-4"); /* 0.000149999... */
  CHECK_STR(pts_number_to_exponential_shortest(123456)->bytes, "1.23456e+5");
  CHECK_STR(pts_number_to_exponential_shortest(0.1)->bytes, "1e-1");
  CHECK_STR(pts_number_to_exponential_shortest(-0.0)->bytes, "0e+0");
  CHECK_STR(pts_number_to_precision(123.456, 4)->bytes, "123.5");
  CHECK_STR(pts_number_to_precision(123.456, 2)->bytes, "1.2e+2");
  CHECK_STR(pts_number_to_precision(0.000123, 2)->bytes, "0.00012");
  CHECK_STR(pts_number_to_precision(1e-7, 2)->bytes, "1.0e-7");
  CHECK_STR(pts_number_to_precision(0, 3)->bytes, "0.00");
  CHECK_STR(pts_number_to_precision(99.99, 3)->bytes, "100");
  CHECK_STR(pts_number_to_precision(5, 1)->bytes, "5");
}

static void radix_strings_are_v8s(void) {
  CHECK_STR(pts_number_to_string_radix(255, 16)->bytes, "ff");
  CHECK_STR(pts_number_to_string_radix(-255, 2)->bytes, "-11111111");
  CHECK_STR(pts_number_to_string_radix(0.5, 2)->bytes, "0.1");
  CHECK_STR(pts_number_to_string_radix(0.1, 3)->bytes, "0.0022002200220022002200220022002201");
  CHECK_STR(pts_number_to_string_radix(1e21, 36)->bytes, "5v1j4f4ds7c000");
  CHECK_STR(pts_number_to_string_radix(-0.0, 16)->bytes, "0");
  CHECK_STR(pts_number_to_string_radix(INFINITY, 16)->bytes, "Infinity");
}

static void locale_strings_group_thousands(void) {
  CHECK_STR(pts_number_to_locale_string(1234567.891)->bytes, "1,234,567.891");
  CHECK_STR(pts_number_to_locale_string(1.0005)->bytes, "1.001");
  CHECK_STR(pts_number_to_locale_string(999.9995)->bytes, "1,000");
  CHECK_STR(pts_number_to_locale_string(0.0001)->bytes, "0");
  CHECK_STR(pts_number_to_locale_string(-0.0)->bytes, "-0");
  CHECK_STR(pts_number_to_locale_string(-INFINITY)->bytes, "-\xE2\x88\x9E");
  CHECK_STR(pts_number_to_locale_string(1e21)->bytes, "1,000,000,000,000,000,000,000");
}

static double parse_int(const char *s, double radix) { return pts_parse_int(pts_string_from_cstr(s), radix); }
static double parse_float(const char *s) { return pts_parse_float(pts_string_from_cstr(s)); }

static void parse_int_and_parse_float_read_prefixes(void) {
  CHECK(parse_int("  42px", 0) == 42);
  CHECK(parse_int("-0x1F", 0) == -31);
  CHECK(parse_int("0x1F", 10) == 0);
  CHECK(parse_int("z", 36) == 35);
  CHECK(isnan(parse_int("12", 1)));
  CHECK(isnan(parse_int("", 0)));
  CHECK(signbit(parse_int("-0", 0)));
  CHECK(parse_int("9007199254740993", 0) == 9007199254740992.0);
  CHECK(parse_int("1111111111111111111111111111111111111111111111111111111", 2) == 36028797018963968.0);
  CHECK(parse_float("3.5e2x") == 350);
  CHECK(parse_float(".5") == 0.5);
  CHECK(parse_float("-Infinityx") == -INFINITY);
  CHECK(parse_float("1e") == 1);
  CHECK(parse_float("0x10") == 0);
  CHECK(isnan(parse_float("inf")));
  CHECK(isnan(parse_float(".")));
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  if (argc > 1 && strcmp(argv[1], "--format-stdin") == 0) {
    return pts_runtime_exit(format_stdin());
  }
  formats_like_javascript();
  remainder_follows_javascript();
  power_follows_javascript();
  to_fixed_rounds_the_exact_value_half_up();
  exponential_and_precision_round_half_up();
  radix_strings_are_v8s();
  locale_strings_group_thousands();
  parse_int_and_parse_float_read_prefixes();
  return pts_runtime_exit(CHECK_DONE());
}
