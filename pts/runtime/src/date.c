/*
 * Date (D-087): a time value in milliseconds since the epoch (NaN for an invalid date),
 * with ECMAScript's day and time arithmetic (21.4.1). Local time takes its offsets from
 * the C library's time zone rules (TZ, /etc/localtime), as V8 takes them from ICU's;
 * a time zone's long name, which V8 takes from ICU, comes from a table of common
 * abbreviations, or is ICU's fallback "GMT+hh:mm".
 */
#include "pts.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

const pts_type pts_type_builtin_Date = {
    .kind = PTS_KIND_DATE,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Date",
    .size = sizeof(pts_date),
};

enum { MS_PER_SECOND = 1000, MS_PER_MINUTE = 60000, MS_PER_HOUR = 3600000, MS_PER_DAY = 86400000 };
static const double MAX_TIME = 8.64e15;

static const char *const weekday_names[] = {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"};
static const char *const month_names[] = {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"};

/* ---------------------------------------------------------------- ECMAScript's arithmetic (21.4.1) */

static double positive_mod(double x, double y) {
  double r = fmod(x, y);
  return r < 0 ? r + y : r;
}

static double to_integer(double d) { return d != d ? 0 : trunc(d) + 0.0; }

static double day_of(double t) { return floor(t / MS_PER_DAY); }

static double day_from_year(double y) { return 365 * (y - 1970) + floor((y - 1969) / 4) - floor((y - 1901) / 100) + floor((y - 1601) / 400); }

static bool is_leap_year(double y) { return fmod(y, 4) == 0 && (fmod(y, 100) != 0 || fmod(y, 400) == 0); }

static double year_from_time(double t) {
  double y = floor(day_of(t) / 365.2425) + 1970;
  while (day_from_year(y) * MS_PER_DAY > t) {
    y--;
  }
  while (day_from_year(y + 1) * MS_PER_DAY <= t) {
    y++;
  }
  return y;
}

/* The days before each month, in a common and a leap year. */
static const int days_before_month[2][13] = {
    {0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365},
    {0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335, 366},
};

typedef struct date_parts {
  double year, month, date, hours, minutes, seconds, ms, weekday;
} date_parts;

static date_parts parts_of(double t) {
  date_parts p;
  p.year = year_from_time(t);
  int leap = is_leap_year(p.year);
  int day_in_year = (int)(day_of(t) - day_from_year(p.year));
  int month = 0;
  while (day_in_year >= days_before_month[leap][month + 1]) {
    month++;
  }
  p.month = month;
  p.date = day_in_year - days_before_month[leap][month] + 1;
  double in_day = positive_mod(t, MS_PER_DAY);
  p.hours = floor(in_day / MS_PER_HOUR);
  p.minutes = floor(positive_mod(in_day, MS_PER_HOUR) / MS_PER_MINUTE);
  p.seconds = floor(positive_mod(in_day, MS_PER_MINUTE) / MS_PER_SECOND);
  p.ms = positive_mod(in_day, MS_PER_SECOND);
  p.weekday = positive_mod(day_of(t) + 4, 7);
  return p;
}

static double make_time(double h, double m, double s, double ms) {
  if (!isfinite(h) || !isfinite(m) || !isfinite(s) || !isfinite(ms)) {
    return NAN;
  }
  return ((to_integer(h) * MS_PER_HOUR + to_integer(m) * MS_PER_MINUTE) + to_integer(s) * MS_PER_SECOND) + to_integer(ms);
}

static double make_day(double year, double month, double date) {
  if (!isfinite(year) || !isfinite(month) || !isfinite(date)) {
    return NAN;
  }
  double y = to_integer(year), m = to_integer(month), dt = to_integer(date);
  double ym = y + floor(m / 12);
  if (fabs(ym) > 400000) {
    return NAN; /* far outside the time values a Date can hold */
  }
  int mn = (int)positive_mod(m, 12);
  return day_from_year(ym) + days_before_month[is_leap_year(ym)][mn] + dt - 1;
}

static double make_date(double day, double time) {
  if (!isfinite(day) || !isfinite(time)) {
    return NAN;
  }
  return day * MS_PER_DAY + time;
}

static double time_clip(double t) {
  if (!isfinite(t) || fabs(t) > MAX_TIME) {
    return NAN;
  }
  return to_integer(t);
}

/* ---------------------------------------------------------------- local time */

/* The local time zone's offset from UTC, in milliseconds, at the UTC time t; its abbreviation in *zone if not NULL. */
static double offset_at(double t, char zone[16]) {
  if (zone != NULL) {
    strcpy(zone, "UTC");
  }
  if (!isfinite(t)) {
    return 0;
  }
  time_t seconds = (time_t)floor(t / MS_PER_SECOND);
  struct tm local;
  if (localtime_r(&seconds, &local) == NULL) {
    return 0;
  }
  if (zone != NULL && local.tm_zone != NULL) {
    snprintf(zone, 16, "%s", local.tm_zone);
  }
  return (double)local.tm_gmtoff * MS_PER_SECOND;
}

static double local_time(double t) { return t + offset_at(t, NULL); }

/* UTC(t) of a local time: in a gap or overlap of a transition, the offset before it, as V8 does. */
static double utc_of(double local) {
  if (!isfinite(local)) {
    return NAN;
  }
  double before = offset_at(local, NULL), utc = local - before, after = offset_at(utc, NULL);
  if (after != before && offset_at(local - after, NULL) == after) {
    utc = local - after;
  }
  return utc;
}

/* ICU's long names of the zones whose abbreviation the C library gives; ICU's fallback is "GMT+hh:mm". */
static const struct {
  const char *abbreviation;
  double offset_hours; /* NAN: any */
  const char *name;
} zone_names[] = {
    {"UTC", NAN, "Coordinated Universal Time"}, {"GMT", NAN, "Greenwich Mean Time"}, {"BST", NAN, "British Summer Time"},
    {"IST", 1, "Irish Standard Time"}, {"IST", 5.5, "India Standard Time"}, {"WET", NAN, "Western European Standard Time"},
    {"WEST", NAN, "Western European Summer Time"}, {"CET", NAN, "Central European Standard Time"},
    {"CEST", NAN, "Central European Summer Time"}, {"EET", NAN, "Eastern European Standard Time"},
    {"EEST", NAN, "Eastern European Summer Time"}, {"MSK", NAN, "Moscow Standard Time"}, {"HKT", NAN, "Hong Kong Standard Time"},
    {"CST", 8, "China Standard Time"}, {"CST", -6, "Central Standard Time"}, {"CDT", NAN, "Central Daylight Time"},
    {"JST", NAN, "Japan Standard Time"}, {"KST", NAN, "Korean Standard Time"}, {"SGT", NAN, "Singapore Standard Time"},
    {"AEST", NAN, "Australian Eastern Standard Time"}, {"AEDT", NAN, "Australian Eastern Daylight Time"},
    {"ACST", NAN, "Australian Central Standard Time"}, {"ACDT", NAN, "Australian Central Daylight Time"},
    {"AWST", NAN, "Australian Western Standard Time"}, {"NZST", NAN, "New Zealand Standard Time"},
    {"NZDT", NAN, "New Zealand Daylight Time"}, {"EST", NAN, "Eastern Standard Time"}, {"EDT", NAN, "Eastern Daylight Time"},
    {"MST", NAN, "Mountain Standard Time"}, {"MDT", NAN, "Mountain Daylight Time"}, {"PST", NAN, "Pacific Standard Time"},
    {"PDT", NAN, "Pacific Daylight Time"}, {"AKST", NAN, "Alaska Standard Time"}, {"AKDT", NAN, "Alaska Daylight Time"},
    {"HST", NAN, "Hawaii-Aleutian Standard Time"}, {"SAST", NAN, "South Africa Standard Time"},
};

static void zone_name(char *out, size_t size, const char *abbreviation, double offset) {
  for (size_t i = 0; i < sizeof zone_names / sizeof zone_names[0]; i++) {
    double hours = zone_names[i].offset_hours;
    if (strcmp(zone_names[i].abbreviation, abbreviation) == 0 && (hours != hours || hours * MS_PER_HOUR == offset)) {
      snprintf(out, size, "%s", zone_names[i].name);
      return;
    }
  }
  int minutes = (int)(fabs(offset) / MS_PER_MINUTE);
  snprintf(out, size, "GMT%c%02d:%02d", offset < 0 ? '-' : '+', minutes / 60, minutes % 60);
}

/* ---------------------------------------------------------------- making dates */

static pts_date *new_date(double time) {
  pts_date *date = pts_object_new(&pts_type_builtin_Date);
  date->time = time;
  return date;
}

double pts_date_now(void) {
  struct timespec now;
  clock_gettime(CLOCK_REALTIME, &now);
  return (double)now.tv_sec * MS_PER_SECOND + (double)(now.tv_nsec / 1000000);
}

pts_date *pts_date_new_now(void) { return new_date(pts_date_now()); }

pts_date *pts_date_new_time(double time) { return new_date(time_clip(time)); }

pts_date *pts_date_new_string(pts_string *text) { return new_date(pts_date_parse(text)); }

pts_date *pts_date_new_value(pts_value value) {
  if (pts_value_is_object(value)) {
    const pts_object *object = pts_value_as_object(value);
    if (object->type->kind == PTS_KIND_DATE) {
      return new_date(((const pts_date *)object)->time);
    }
    if (object->type->kind == PTS_KIND_STRING) {
      return pts_date_new_string((pts_string *)object);
    }
  }
  return pts_date_new_time(pts_value_to_number(value));
}

/* The time value of components (year, month[, date, hours, minutes, seconds, ms]): argc of them given, the rest defaults. */
static double time_of_components(double argc, double year, double month, double date, double hours, double minutes, double seconds, double ms) {
  double values[7] = {year, month, date, hours, minutes, seconds, ms};
  static const double defaults[7] = {NAN, 0, 1, 0, 0, 0, 0};
  for (int i = (int)argc; i < 7; i++) {
    values[i] = defaults[i];
  }
  double y = values[0];
  if (y == y) {
    double integer = to_integer(y);
    if (integer >= 0 && integer <= 99) {
      y = 1900 + integer; /* a two-digit year */
    }
  }
  return make_date(make_day(y, values[1], values[2]), make_time(values[3], values[4], values[5], values[6]));
}

pts_date *pts_date_new_components(double argc, double year, double month, double date, double hours, double minutes, double seconds, double ms) {
  return new_date(time_clip(utc_of(time_of_components(argc, year, month, date, hours, minutes, seconds, ms))));
}

double pts_date_utc(double argc, double year, double month, double date, double hours, double minutes, double seconds, double ms) {
  return time_clip(time_of_components(argc < 1 ? 1 : argc, year, month, date, hours, minutes, seconds, ms));
}

/* ---------------------------------------------------------------- reading and writing fields */

double pts_date_get(pts_date *date, double field) {
  double t = date->time;
  if (field == PTS_DATE_TIME) {
    return t;
  }
  if (t != t) {
    return NAN;
  }
  if (field == PTS_DATE_TIMEZONE_OFFSET) {
    return (t - local_time(t)) / MS_PER_MINUTE;
  }
  bool utc = field >= PTS_DATE_UTC_FULL_YEAR;
  date_parts p = parts_of(utc ? t : local_time(t));
  switch ((int)field - (utc ? PTS_DATE_UTC_FULL_YEAR - PTS_DATE_FULL_YEAR : 0)) {
  case PTS_DATE_FULL_YEAR:
    return p.year;
  case PTS_DATE_MONTH:
    return p.month;
  case PTS_DATE_DATE:
    return p.date;
  case PTS_DATE_DAY:
    return p.weekday;
  case PTS_DATE_HOURS:
    return p.hours;
  case PTS_DATE_MINUTES:
    return p.minutes;
  case PTS_DATE_SECONDS:
    return p.seconds;
  default:
    return p.ms;
  }
}

double pts_date_set_time(pts_date *date, double time) { return date->time = time_clip(time); }

/*
 * The setters (setFullYear … setMilliseconds, local or UTC): field is the first component
 * set; argc of a, b, c, d were given, the rest keep the date's components. setFullYear on
 * an invalid date starts from +0, as the spec says; the others leave it invalid.
 */
double pts_date_set(pts_date *date, double field, double argc, double a, double b, double c, double d) {
  bool utc = field >= PTS_DATE_UTC_FULL_YEAR;
  int first = (int)field - (utc ? PTS_DATE_UTC_FULL_YEAR - PTS_DATE_FULL_YEAR : 0);
  double t = date->time;
  if (t != t && first != PTS_DATE_FULL_YEAR) {
    return NAN;
  }
  double local = t != t ? 0 : utc ? t : local_time(t);
  date_parts p = parts_of(local);
  double values[7] = {p.year, p.month, p.date, p.hours, p.minutes, p.seconds, p.ms};
  int index = first == PTS_DATE_FULL_YEAR ? 0 : first == PTS_DATE_MONTH ? 1 : first == PTS_DATE_DATE ? 2 : first - PTS_DATE_HOURS + 3;
  double given[4] = {a, b, c, d};
  int limit = index < 3 ? 3 : 7; /* the date setters set date components, the time setters time components */
  for (int i = 0; i < (int)argc && index + i < limit; i++) {
    values[index + i] = given[i];
  }
  double result = make_date(make_day(values[0], values[1], values[2]), make_time(values[3], values[4], values[5], values[6]));
  return date->time = time_clip(utc ? result : utc_of(result));
}

/* ---------------------------------------------------------------- formatting */

static int put_year(char *out, double year) {
  if (year >= 0) {
    return sprintf(out, "%04d", (int)year);
  }
  return sprintf(out, "-%06d", (int)-year);
}

static pts_string *string_of(const char *text) { return pts_string_from_cstr(text); }

/* "GMT+0800 (Hong Kong Standard Time)" for the local zone at the UTC time t. */
static int put_zone(char *out, double t) {
  char abbreviation[16], name[64];
  double offset = offset_at(t, abbreviation);
  zone_name(name, sizeof name, abbreviation, offset);
  int minutes = (int)(fabs(offset) / MS_PER_MINUTE);
  return sprintf(out, "GMT%c%02d%02d (%s)", offset < 0 ? '-' : '+', minutes / 60, minutes % 60, name);
}

pts_string *pts_date_format(pts_date *date, double format) {
  double t = date->time;
  if (t != t) {
    if (format == PTS_DATE_ISO) {
      pts_throw_new(&pts_type_builtin_RangeError, "Invalid time value");
    }
    return string_of("Invalid Date");
  }
  char out[160], *o = out;
  if (format == PTS_DATE_ISO) {
    date_parts p = parts_of(t);
    if (p.year >= 0 && p.year <= 9999) {
      o += sprintf(o, "%04d", (int)p.year);
    } else {
      o += sprintf(o, "%c%06d", p.year < 0 ? '-' : '+', (int)fabs(p.year));
    }
    sprintf(o, "-%02d-%02dT%02d:%02d:%02d.%03dZ", (int)p.month + 1, (int)p.date, (int)p.hours, (int)p.minutes, (int)p.seconds, (int)p.ms);
    return string_of(out);
  }
  if (format == PTS_DATE_UTC) {
    date_parts p = parts_of(t);
    o += sprintf(o, "%s, %02d %s ", weekday_names[(int)p.weekday], (int)p.date, month_names[(int)p.month]);
    o += put_year(o, p.year);
    sprintf(o, " %02d:%02d:%02d GMT", (int)p.hours, (int)p.minutes, (int)p.seconds);
    return string_of(out);
  }
  date_parts p = parts_of(local_time(t));
  if (format != PTS_DATE_TIME_ONLY) {
    o += sprintf(o, "%s %s %02d ", weekday_names[(int)p.weekday], month_names[(int)p.month], (int)p.date);
    o += put_year(o, p.year);
    if (format == PTS_DATE_DATE_ONLY) {
      return string_of(out);
    }
    *o++ = ' ';
  }
  o += sprintf(o, "%02d:%02d:%02d ", (int)p.hours, (int)p.minutes, (int)p.seconds);
  put_zone(o, t);
  return string_of(out);
}

pts_value pts_date_to_json(pts_date *date) {
  return date->time != date->time ? PTS_VALUE_NULL : pts_value_from_string(pts_date_format(date, PTS_DATE_ISO));
}

/* ---------------------------------------------------------------- parsing */

typedef struct scanner {
  const char *s, *end;
} scanner;

static bool at(scanner *in, char c) {
  if (in->s < in->end && *in->s == c) {
    in->s++;
    return true;
  }
  return false;
}

static bool is_digit(char c) { return c >= '0' && c <= '9'; }

/* Reads exactly n digits (or, with n 0, one or more) into *value. */
static bool digits(scanner *in, int n, double *value, int *count) {
  double v = 0;
  int read = 0;
  while (in->s < in->end && is_digit(*in->s) && (n == 0 || read < n)) {
    v = v * 10 + (*in->s++ - '0');
    read++;
  }
  if (count != NULL) {
    *count = read;
  }
  *value = v;
  return n == 0 ? read > 0 : read == n;
}

/* ECMAScript's date time string format (21.4.1.32): NaN if s isn't one. */
static double parse_iso(const char *s, size_t n) {
  scanner in = {s, s + n};
  double year, month = 1, day = 1, hours = 0, minutes = 0, seconds = 0, ms = 0;
  double sign = 1;
  if (at(&in, '+') || (in.s < in.end && *in.s == '-' && (sign = -1, in.s++, true))) {
    if (!digits(&in, 6, &year, NULL) || (sign < 0 && year == 0)) {
      return NAN;
    }
    year *= sign;
  } else if (!digits(&in, 4, &year, NULL)) {
    return NAN;
  }
  bool date_only = true, has_offset = false;
  double offset = 0;
  if (at(&in, '-')) {
    if (!digits(&in, 2, &month, NULL)) {
      return NAN;
    }
    if (at(&in, '-') && !digits(&in, 2, &day, NULL)) {
      return NAN;
    }
  }
  if (at(&in, 'T') || at(&in, 't') || at(&in, ' ')) {
    date_only = false;
    if (!digits(&in, 2, &hours, NULL) || !at(&in, ':') || !digits(&in, 2, &minutes, NULL)) {
      return NAN;
    }
    if (at(&in, ':')) {
      if (!digits(&in, 2, &seconds, NULL)) {
        return NAN;
      }
      if (at(&in, '.') || at(&in, ',')) {
        int count;
        double fraction;
        if (!digits(&in, 0, &fraction, &count)) {
          return NAN;
        }
        ms = floor(fraction / pow(10, count - 3)); /* milliseconds, the rest cut off */
      }
    }
    if (at(&in, 'Z') || at(&in, 'z')) {
      has_offset = true;
    } else if (in.s < in.end && (*in.s == '+' || *in.s == '-')) {
      double offset_sign = *in.s++ == '-' ? -1 : 1, offset_hours, offset_minutes;
      if (!digits(&in, 2, &offset_hours, NULL) || !at(&in, ':') || !digits(&in, 2, &offset_minutes, NULL)) {
        return NAN;
      }
      has_offset = true;
      offset = offset_sign * (offset_hours * MS_PER_HOUR + offset_minutes * MS_PER_MINUTE);
    }
  } else if (at(&in, 'Z')) {
    has_offset = true;
  }
  if (in.s != in.end || month < 1 || month > 12 || day < 1 || day > 31 || hours > 24 || minutes > 59 || seconds > 59 ||
      (hours == 24 && (minutes > 0 || seconds > 0 || ms > 0))) {
    return NAN;
  }
  double t = make_date(make_day(year, month - 1, day), make_time(hours, minutes, seconds, ms));
  if (date_only || has_offset) {
    return time_clip(t - offset);
  }
  return time_clip(utc_of(t));
}

/* The month a word names (its first three letters), or -1. */
static int month_named(const char *word, size_t n) {
  for (int i = 0; i < 12 && n >= 3; i++) {
    if (strncasecmp(word, month_names[i], 3) == 0) {
      return i;
    }
  }
  return -1;
}

/*
 * The other formats V8 reads that dates print or people commonly write: "Jan 15 2024",
 * "January 15, 2024 10:30:00", "15 Jan 2024", "Mon Jan 15 2024 10:30:05 GMT+0800 (…)",
 * "Mon, 15 Jan 2024 02:30:05 GMT", "2024/01/15 10:30", "1/15/2024", with AM and PM.
 * Without a zone the time is local. NaN for anything else.
 */
static double parse_legacy(const char *s, size_t n) {
  double numbers[3];
  int number_count = 0, month = -1;
  double hours = -1, minutes = 0, seconds = 0, ms = 0, offset = 0;
  bool has_offset = false, pm = false, am = false;
  const char *p = s, *end = s + n;
  while (p < end) {
    char c = *p;
    if (c == '(') { /* a comment, such as the zone's name */
      while (p < end && *p != ')') {
        p++;
      }
      p++;
      continue;
    }
    if ((c | 0x20) >= 'a' && (c | 0x20) <= 'z') {
      const char *word = p;
      while (p < end && (*p | 0x20) >= 'a' && (*p | 0x20) <= 'z') {
        p++;
      }
      size_t length = (size_t)(p - word);
      if ((length == 3 && (strncasecmp(word, "GMT", 3) == 0 || strncasecmp(word, "UTC", 3) == 0)) || (length == 1 && (*word | 0x20) == 'z')) {
        has_offset = true;
      } else if (length == 2 && strncasecmp(word, "am", 2) == 0) {
        am = true;
      } else if (length == 2 && strncasecmp(word, "pm", 2) == 0) {
        pm = true;
      } else if (month < 0 && month_named(word, length) >= 0) {
        month = month_named(word, length);
      } else if (length < 3 || (month_named(word, length) < 0 && strncasecmp(word, "mon", 3) && strncasecmp(word, "tue", 3) &&
                                strncasecmp(word, "wed", 3) && strncasecmp(word, "thu", 3) && strncasecmp(word, "fri", 3) &&
                                strncasecmp(word, "sat", 3) && strncasecmp(word, "sun", 3))) {
        return NAN;
      }
      continue;
    }
    if ((c == '+' || c == '-') && has_offset && p + 1 < end && is_digit(p[1])) {
      double sign = c == '-' ? -1 : 1, value = 0;
      int count = 0;
      for (p++; p < end && is_digit(*p); p++, count++) {
        value = value * 10 + (*p - '0');
      }
      double h = count <= 2 ? value : floor(value / 100), m = count <= 2 ? 0 : fmod(value, 100);
      offset = sign * (h * MS_PER_HOUR + m * MS_PER_MINUTE);
      continue;
    }
    if (is_digit(c)) {
      double value = 0;
      for (; p < end && is_digit(*p); p++) {
        value = value * 10 + (*p - '0');
      }
      if (p < end && *p == ':' && hours < 0) { /* a time: h:mm[:ss[.mmm]] */
        hours = value;
        scanner in = {p + 1, end};
        if (!digits(&in, 0, &minutes, NULL)) {
          return NAN;
        }
        if (at(&in, ':') && !digits(&in, 0, &seconds, NULL)) {
          return NAN;
        }
        if (at(&in, '.')) {
          int count;
          double fraction;
          if (!digits(&in, 0, &fraction, &count)) {
            return NAN;
          }
          ms = floor(fraction / pow(10, count - 3));
        }
        p = in.s;
        continue;
      }
      if (number_count == 3) {
        return NAN;
      }
      numbers[number_count++] = value;
      continue;
    }
    if (c == ' ' || c == ',' || c == '/' || c == '-' || c == '.') {
      p++;
      continue;
    }
    return NAN;
  }
  double year, day;
  if (month >= 0) { /* "Jan 15 2024", "15 Jan 2024" */
    if (number_count != 2) {
      return NAN;
    }
    bool year_first = numbers[0] > 31;
    year = year_first ? numbers[0] : numbers[1];
    day = year_first ? numbers[1] : numbers[0];
  } else if (number_count == 3) { /* "2024/01/15" or "1/15/2024" */
    bool year_first = numbers[0] > 31;
    year = year_first ? numbers[0] : numbers[2];
    month = (int)(year_first ? numbers[1] : numbers[0]) - 1;
    day = year_first ? numbers[2] : numbers[1];
  } else {
    return NAN;
  }
  if (year >= 0 && year <= 49) {
    year += 2000;
  } else if (year >= 50 && year <= 99) {
    year += 1900;
  }
  if (hours < 0) {
    hours = 0;
  }
  if (pm && hours < 12) {
    hours += 12;
  } else if (am && hours == 12) {
    hours = 0;
  }
  if (month < 0 || month > 11 || day < 1 || day > 31 || hours > 24 || minutes > 59 || seconds > 59) {
    return NAN;
  }
  double t = make_date(make_day(year, month, day), make_time(hours, minutes, seconds, ms));
  return time_clip(has_offset ? t - offset : utc_of(t));
}

double pts_date_parse(pts_string *text) {
  const char *s = text->bytes;
  size_t n = text->byte_len;
  while (n > 0 && (*s == ' ' || *s == '\t' || *s == '\n')) {
    s++, n--;
  }
  while (n > 0 && (s[n - 1] == ' ' || s[n - 1] == '\t' || s[n - 1] == '\n')) {
    n--;
  }
  double t = parse_iso(s, n);
  return t == t ? t : parse_legacy(s, n);
}
