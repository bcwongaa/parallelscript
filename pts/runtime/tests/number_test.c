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

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  if (argc > 1 && strcmp(argv[1], "--format-stdin") == 0) {
    return pts_runtime_exit(format_stdin());
  }
  formats_like_javascript();
  return pts_runtime_exit(CHECK_DONE());
}
