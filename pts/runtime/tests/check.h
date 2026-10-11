/* A minimal assertion helper for the runtime's C unit tests. Each test file is its own program. */
#ifndef PTS_TEST_CHECK_H
#define PTS_TEST_CHECK_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int check_failures = 0;

#define CHECK(cond)                                                                    \
  do {                                                                                 \
    if (!(cond)) {                                                                     \
      fprintf(stderr, "%s:%d: CHECK failed: %s\n", __FILE__, __LINE__, #cond);       \
      check_failures++;                                                                \
    }                                                                                  \
  } while (0)

#define CHECK_STR(actual, expected)                                                                     \
  do {                                                                                                  \
    const char *check_actual_ = (actual);                                                               \
    const char *check_expected_ = (expected);                                                           \
    if (strcmp(check_actual_, check_expected_) != 0) {                                                  \
      fprintf(stderr, "%s:%d: expected \"%s\", got \"%s\"\n", __FILE__, __LINE__, check_expected_, check_actual_); \
      check_failures++;                                                                                 \
    }                                                                                                   \
  } while (0)

#define CHECK_DONE() (check_failures == 0 ? 0 : (fprintf(stderr, "%d check(s) failed\n", check_failures), 1))

#endif
