#include "pts.h"

#include <math.h>
#include <stdlib.h>
#include <time.h>

#include "check.h"

/* Expected values are what Node gives with TZ=America/New_York. */

static double parse(const char *text) { return pts_date_parse(pts_string_from_cstr(text)); }

static const char *format(pts_date *date, int which) { return pts_date_format(date, which)->bytes; }

static void components_are_local_time(void) {
  pts_date *date = pts_date_new_components(7, 2024, 0, 15, 10, 30, 5, 7);
  CHECK(date->time == 1705332605007.0);
  CHECK_STR(format(date, PTS_DATE_ISO), "2024-01-15T15:30:05.007Z");
  CHECK_STR(format(date, PTS_DATE_STRING), "Mon Jan 15 2024 10:30:05 GMT-0500 (Eastern Standard Time)");
  CHECK_STR(format(date, PTS_DATE_UTC), "Mon, 15 Jan 2024 15:30:05 GMT");
  CHECK(pts_date_get(date, PTS_DATE_HOURS) == 10);
  CHECK(pts_date_get(date, PTS_DATE_UTC_FULL_YEAR + PTS_DATE_HOURS - PTS_DATE_FULL_YEAR) == 15);
  CHECK(pts_date_get(date, PTS_DATE_DAY) == 1);
  CHECK(pts_date_get(date, PTS_DATE_TIMEZONE_OFFSET) == 300);
  pts_date *summer = pts_date_new_components(2, 99, 6, 0, 0, 0, 0, 0);
  CHECK_STR(format(summer, PTS_DATE_STRING), "Thu Jul 01 1999 00:00:00 GMT-0400 (Eastern Daylight Time)");
}

static void gaps_take_the_offset_before_the_transition(void) {
  CHECK_STR(format(pts_date_new_components(5, 2024, 2, 10, 2, 30, 0, 0), PTS_DATE_STRING), "Sun Mar 10 2024 03:30:00 GMT-0400 (Eastern Daylight Time)");
  CHECK_STR(format(pts_date_new_components(5, 2024, 10, 3, 1, 30, 0, 0), PTS_DATE_STRING), "Sun Nov 03 2024 01:30:00 GMT-0400 (Eastern Daylight Time)");
}

static void setters_keep_the_other_components(void) {
  pts_date *date = pts_date_new_time(0);
  pts_date_set(date, PTS_DATE_UTC_FULL_YEAR, 3, 2000, 1, 29, 0);
  CHECK_STR(format(date, PTS_DATE_ISO), "2000-02-29T00:00:00.000Z");
  pts_date_set(date, PTS_DATE_UTC_FULL_YEAR + PTS_DATE_MONTH - PTS_DATE_FULL_YEAR, 1, 13, 0, 0, 0);
  CHECK_STR(format(date, PTS_DATE_ISO), "2001-03-01T00:00:00.000Z"); /* February 29th of 2001 is March 1st */
  pts_date *invalid = pts_date_new_time(NAN);
  CHECK(isnan(pts_date_set(invalid, PTS_DATE_HOURS, 1, 5, 0, 0, 0)));
  CHECK(!isnan(pts_date_set(invalid, PTS_DATE_FULL_YEAR, 1, 2020, 0, 0, 0)));
}

static void times_clip_and_print_extended_years(void) {
  CHECK(isnan(pts_date_new_time(8.64e15 + 1)->time));
  CHECK_STR(format(pts_date_new_time(8.64e15), PTS_DATE_ISO), "+275760-09-13T00:00:00.000Z");
  CHECK_STR(format(pts_date_new_time(-62198755200000.0), PTS_DATE_ISO), "-000001-01-01T00:00:00.000Z");
  CHECK_STR(format(pts_date_new_time(NAN), PTS_DATE_STRING), "Invalid Date");
  CHECK(pts_date_to_json(pts_date_new_time(NAN)) == PTS_VALUE_NULL);
}

static void parsing_reads_iso_and_common_formats(void) {
  CHECK(parse("2024-01-15") == 1705276800000.0);
  CHECK(parse("2024-01-15T10:30") == 1705332600000.0);
  CHECK(parse("2024-01-15T10:30:05.123Z") == 1705314605123.0);
  CHECK(parse("2024-01-15T10:30:05+08:00") == 1705285805000.0);
  CHECK(parse("+002024-01") == 1704067200000.0);
  CHECK(isnan(parse("2024-13-01")));
  CHECK(parse("2023-02-29") == 1677628800000.0); /* V8 doesn't check the day against the month */
  CHECK(parse("Jan 15, 2024") == 1705294800000.0);
  CHECK(parse("15 January 2024 10:30 PM") == 1705375800000.0);
  CHECK(parse("2024/01/15") == 1705294800000.0);
  CHECK(parse("1/15/2024") == 1705294800000.0);
  CHECK(parse("Mon Jan 15 2024 10:30:05 GMT+0800 (Hong Kong Standard Time)") == 1705285805000.0);
  CHECK(parse("Mon, 15 Jan 2024 02:30:05 GMT") == 1705285805000.0);
  CHECK(isnan(parse("not a date")));
  CHECK(isnan(parse("")));
}

int main(int argc, char **argv) {
  setenv("TZ", "America/New_York", 1);
  tzset();
  pts_runtime_init(argc, argv);
  components_are_local_time();
  gaps_take_the_offset_before_the_transition();
  setters_keep_the_other_components();
  times_clip_and_print_extended_years();
  parsing_reads_iso_and_common_formats();
  return pts_runtime_exit(CHECK_DONE());
}
