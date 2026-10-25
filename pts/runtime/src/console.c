/*
 * The console object: a port of Node's Console (lib/internal/console/constructor.js,
 * D-088). log, info, debug and dirxml write to stdout, error and warn to stderr, each
 * formatting its arguments as util.format does (inspect.c); every line is indented by
 * the groups that are open.
 */
#include "inspect_internal.h"
#include "string_internal.h"

#include <errno.h>
#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

enum { GROUP_INDENTATION = 2 };

/* The indentation of the open groups (Node's kGroupIndentationString), one for the program. */
static atomic_size_t group_indentation;

static void indent(FILE *stream, size_t count) {
  for (size_t i = 0; i < count; i++) {
    fputc(' ', stream);
  }
}

/* Writes WTF-8 text as UTF-8, a lone surrogate as U+FFFD, as Node's streams encode strings. */
static void write_text(FILE *stream, const char *bytes, size_t len) {
  size_t start = 0;
  for (size_t i = 0; i < len;) {
    if (pts_wtf8_surrogate_at(bytes, len, i, 0xA0, 0xBF)) {
      fwrite(bytes + start, 1, i - start, stream);
      fwrite("\xEF\xBF\xBD", 1, 3, stream);
      i += PTS_SURROGATE_BYTES;
      start = i;
    } else {
      i++;
    }
  }
  fwrite(bytes + start, 1, len - start, stream);
}

/*
 * Node's kWriteToConsole: the text, every line indented by the open groups, and a line
 * break, written at once. stdout is flushed before stderr is written, so that the two
 * keep the program's order where they go to one file, as Node's synchronous writes do.
 */
static void write_line(FILE *stream, const char *bytes, size_t len) {
  if (stream == stderr) {
    pts_flush_output(stdout);
  }
  size_t indentation = atomic_load(&group_indentation);
  errno = 0;
  flockfile(stream);
  indent(stream, indentation);
  for (const char *newline; indentation > 0 && (newline = memchr(bytes, '\n', len)) != NULL;) {
    size_t line = (size_t)(newline - bytes) + 1;
    write_text(stream, bytes, line);
    indent(stream, indentation);
    bytes += line, len -= line;
  }
  write_text(stream, bytes, len);
  fputc('\n', stream);
  funlockfile(stream);
  pts_check_output(stream);
}

/* Writes the arguments to stream as util.format formats them. */
static void write_formatted(FILE *stream, size_t argc, const pts_value *argv) {
  size_t len;
  char *bytes = pts_format(argc, argv, &len);
  write_line(stream, bytes, len);
  free(bytes);
}

/* process.emitWarning's line on stderr (Node prefixes "(node:<pid>) "). */
static void warn(const char *format, const char *label, const char *method) {
  char line[256];
  int len = snprintf(line, sizeof line, format, label, method);
  pts_flush_output(stdout);
  fprintf(stderr, "Warning: %.*s\n", len, line);
}

void pts_console_log(size_t argc, const pts_value *argv) { write_formatted(stdout, argc, argv); }

/* The arguments with each spread one (an array, boxed as an object) replaced by its elements (D-057). */
static pts_value *spread_arguments(size_t argc, const pts_value *argv, const bool *spread, size_t *count) {
  size_t n = 0;
  for (size_t i = 0; i < argc; i++) {
    n += spread[i] ? pts_array_length_of((const pts_array *)pts_value_as_object(argv[i])) : 1;
  }
  pts_value *values = GC_MALLOC(n * sizeof *values + 1);
  *count = 0;
  for (size_t i = 0; i < argc; i++) {
    if (!spread[i]) {
      values[(*count)++] = argv[i];
      continue;
    }
    const pts_array *array = (const pts_array *)pts_value_as_object(argv[i]);
    for (size_t j = 0; j < pts_array_length_of(array) && *count < n; j++) { // the length now, as JS's array iterator reads it
      values[(*count)++] = pts_array_element(array, (double)j);
    }
  }
  return values;
}

void pts_console_log_spread(size_t argc, const pts_value *argv, const bool *spread) { pts_console(PTS_CONSOLE_LOG, argc, argv, spread); }

/* ---------------------------------------------------------------- counts and timers */

/* A label's count or start time, in a list that one lock guards: programs use a few labels. */
typedef struct label_entry {
  struct label_entry *next;
  char *label;
  double value;
} label_entry;

static label_entry *counts, *timers;
static pthread_mutex_t labels_lock = PTHREAD_MUTEX_INITIALIZER;

static label_entry **find_label(label_entry **list, const char *label) {
  while (*list != NULL && strcmp((*list)->label, label) != 0) {
    list = &(*list)->next;
  }
  return list;
}

static void add_label(label_entry **list, const char *label, double value) {
  label_entry *entry = malloc(sizeof *entry);
  char *copy = strdup(label);
  if (entry == NULL || copy == NULL) {
    pts_panic("RangeError", "Out of memory keeping a console label");
  }
  *entry = (label_entry){.next = *list, .label = copy, .value = value};
  *list = entry;
}

static void remove_label(label_entry **slot) {
  label_entry *entry = *slot;
  *slot = entry->next;
  free(entry->label);
  free(entry);
}

/* The label argument, `${label}` or "default" for undefined or none, as a C string the caller frees. */
static char *label_of(size_t argc, const pts_value *argv) {
  char *label;
  if (argc == 0 || argv[0] == PTS_VALUE_UNDEFINED) {
    label = strdup("default");
  } else {
    pts_string *string = pts_value_to_string(argv[0]);
    label = strndup(string->bytes, string->byte_len);
  }
  if (label == NULL) {
    pts_panic("RangeError", "Out of memory keeping a console label");
  }
  return label;
}

static void count(const char *label) {
  pthread_mutex_lock(&labels_lock);
  label_entry **slot = find_label(&counts, label);
  double n = 1;
  if (*slot == NULL) {
    add_label(&counts, label, n);
  } else {
    n = ++(*slot)->value;
  }
  pthread_mutex_unlock(&labels_lock);
  char line[64];
  snprintf(line, sizeof line, ": %.0f", n);
  pts_string *text = pts_string_concat(pts_string_from_cstr(label), pts_string_from_cstr(line));
  write_line(stdout, text->bytes, text->byte_len);
}

static void count_reset(const char *label) {
  pthread_mutex_lock(&labels_lock);
  label_entry **slot = find_label(&counts, label);
  bool found = *slot != NULL;
  if (found) {
    remove_label(slot);
  }
  pthread_mutex_unlock(&labels_lock);
  if (!found) {
    warn("Count for '%s' does not exist%s", label, "");
  }
}

static double now_ms(void) {
  struct timespec now;
  clock_gettime(CLOCK_MONOTONIC, &now);
  return (double)now.tv_sec * 1000 + (double)now.tv_nsec / 1e6;
}

static void start_timer(const char *label) {
  pthread_mutex_lock(&labels_lock);
  bool exists = *find_label(&timers, label) != NULL;
  if (!exists) {
    add_label(&timers, label, now_ms());
  }
  pthread_mutex_unlock(&labels_lock);
  if (exists) {
    warn("Label '%s' already exists for %s", label, "console.time()");
  }
}

/* x.toFixed(3) into buffer. */
static void to_fixed_3(char *buffer, size_t size, double x) {
  pts_string *fixed = pts_number_to_fixed(x, 3);
  snprintf(buffer, size, "%.*s", (int)fixed->byte_len, fixed->bytes);
}

/* Node's formatTime: "1.234ms", "2.500s", or "1:02.003 (m:ss.mmm)" from a minute up. */
static void format_time(char *out, size_t size, double ms) {
  enum { SECOND = 1000, MINUTE = 60 * SECOND, HOUR = 60 * MINUTE };
  double hours = 0, minutes = 0, seconds = 0;
  if (ms >= SECOND) {
    if (ms >= MINUTE) {
      if (ms >= HOUR) {
        hours = floor(ms / HOUR);
        ms = fmod(ms, HOUR);
      }
      minutes = floor(ms / MINUTE);
      ms = fmod(ms, MINUTE);
    }
    seconds = ms / SECOND;
  }
  char fixed[64];
  if (hours != 0 || minutes != 0) {
    to_fixed_3(fixed, sizeof fixed, seconds);
    char *point = strchr(fixed, '.');
    *point = '\0';
    if (hours != 0) {
      snprintf(out, size, "%.0f:%02.0f:%02d.%s (h:mm:ss.mmm)", hours, minutes, atoi(fixed), point + 1);
    } else {
      snprintf(out, size, "%.0f:%02d.%s (m:ss.mmm)", minutes, atoi(fixed), point + 1);
    }
    return;
  }
  if (seconds != 0) {
    to_fixed_3(fixed, sizeof fixed, seconds);
    snprintf(out, size, "%ss", fixed);
    return;
  }
  to_fixed_3(fixed, sizeof fixed, ms);
  char shortest[PTS_NUMBER_BUF_SIZE];
  size_t len = pts_number_format(strtod(fixed, NULL), shortest);
  snprintf(out, size, "%.*sms", (int)len, shortest);
}

/* timeEnd and timeLog: `label: <time>` and any other arguments, through console.log('%s: %s', …); timeEnd stops the timer. */
static void log_timer(const char *label, bool end, size_t argc, const pts_value *argv) {
  pthread_mutex_lock(&labels_lock);
  label_entry **slot = find_label(&timers, label);
  bool found = *slot != NULL;
  double start = found ? (*slot)->value : 0;
  if (found && end) {
    remove_label(slot);
  }
  pthread_mutex_unlock(&labels_lock);
  if (!found) {
    warn("No such label '%s' for %s", label, end ? "console.timeEnd()" : "console.timeLog()");
    return;
  }
  char elapsed[96];
  format_time(elapsed, sizeof elapsed, now_ms() - start);
  size_t n = 3 + (argc > 1 ? argc - 1 : 0);
  pts_value *values = GC_MALLOC(n * sizeof *values); /* the collector sees the strings in it */
  values[0] = pts_value_from_string(pts_string_from_cstr("%s: %s"));
  values[1] = pts_value_from_string(pts_string_from_cstr(label));
  values[2] = pts_value_from_string(pts_string_from_cstr(elapsed));
  for (size_t i = 1; i < argc; i++) {
    values[2 + i] = argv[i];
  }
  write_formatted(stdout, n, values);
}

/* ---------------------------------------------------------------- the methods */

/* console.assert(condition, ...data): if the condition is falsy, "Assertion failed" and the data, through console.warn. */
static void assert_value(size_t argc, const pts_value *argv) {
  if (argc > 0 && pts_value_truthy(argv[0])) {
    return;
  }
  size_t n = argc > 1 ? argc - 1 : 0;
  pts_value *values = GC_MALLOC((n + 1) * sizeof *values); /* the collector sees the strings in it */
  pts_string *intro = pts_string_from_cstr("Assertion failed");
  if (n > 0 && pts_value_is_object(argv[1]) && pts_value_as_object(argv[1])->type->kind == PTS_KIND_STRING) {
    memcpy(values, argv + 1, n * sizeof *values);
    values[0] = pts_value_from_string(pts_string_concat(pts_string_concat(intro, pts_string_from_cstr(": ")),
                                                        (pts_string *)pts_value_as_object(argv[1])));
  } else {
    values[0] = pts_value_from_string(intro);
    memcpy(values + 1, argv + 1, n * sizeof *values);
    n++;
  }
  write_formatted(stderr, n, values);
}

/* console.trace(...data): "Trace: " and the formatted data, then the stack where it was called, on stderr. */
static void trace(size_t argc, const pts_value *argv) {
  size_t len;
  char *message = pts_format(argc, argv, &len);
  pts_error *error = pts_error_new(&pts_type_builtin_Error, pts_string_from_cstr(message));
  free(message);
  error->name = pts_string_from_cstr("Trace");
  pts_string *stack = pts_value_to_string(pts_error_get_stack(error));
  write_line(stderr, stack->bytes, stack->byte_len);
}

/* The methods that take a label first: count, countReset, time, timeEnd and timeLog. */
static void with_label(pts_console_method method, size_t argc, const pts_value *argv) {
  char *label = label_of(argc, argv);
  switch (method) {
  case PTS_CONSOLE_COUNT:
    count(label);
    break;
  case PTS_CONSOLE_COUNT_RESET:
    count_reset(label);
    break;
  case PTS_CONSOLE_TIME:
    start_timer(label);
    break;
  case PTS_CONSOLE_TIME_END:
    log_timer(label, true, 0, NULL);
    break;
  default:
    log_timer(label, false, argc, argv);
    break;
  }
  free(label);
}

void pts_console(pts_console_method method, size_t argc, const pts_value *argv, const bool *spread) {
  if (spread != NULL) {
    argv = spread_arguments(argc, argv, spread, &argc);
  }
  switch (method) {
  case PTS_CONSOLE_LOG:
    write_formatted(stdout, argc, argv);
    break;
  case PTS_CONSOLE_ERROR:
    write_formatted(stderr, argc, argv);
    break;
  case PTS_CONSOLE_GROUP:
    if (argc > 0) {
      write_formatted(stdout, argc, argv);
    }
    atomic_fetch_add(&group_indentation, GROUP_INDENTATION);
    break;
  case PTS_CONSOLE_ASSERT:
    assert_value(argc, argv);
    break;
  case PTS_CONSOLE_TRACE:
    trace(argc, argv);
    break;
  case PTS_CONSOLE_COUNT:
  case PTS_CONSOLE_COUNT_RESET:
  case PTS_CONSOLE_TIME:
  case PTS_CONSOLE_TIME_END:
  case PTS_CONSOLE_TIME_LOG:
    with_label(method, argc, argv);
    break;
  }
}

void pts_console_group_end(void) {
  size_t indentation = atomic_load(&group_indentation);
  while (indentation > 0 && !atomic_compare_exchange_weak(&group_indentation, &indentation,
                                                           indentation > GROUP_INDENTATION ? indentation - GROUP_INDENTATION : 0)) {
  }
}

void pts_console_dir(pts_value value, double depth, bool show_hidden) {
  size_t len;
  char *bytes = pts_inspect(value, depth, show_hidden, &len);
  write_line(stdout, bytes, len);
  free(bytes);
}

pts_string *pts_util_inspect(pts_value value) {
  size_t len;
  char *bytes = pts_inspect(value, 2, false, &len);
  pts_string *result = pts_string_from_utf8(bytes, len);
  free(bytes);
  return result;
}

void pts_console_table(pts_value data, pts_value properties) {
  bool tabular = pts_value_is_object(data);
  if (tabular) {
    pts_kind kind = pts_value_as_object(data)->type->kind;
    tabular = kind != PTS_KIND_STRING && kind != PTS_KIND_INT64 && kind != PTS_KIND_UINT64 && kind != PTS_KIND_FUNCTION;
  }
  if (!tabular) { /* Node logs what isn't an object */
    write_formatted(stdout, 1, &data);
    return;
  }
  size_t len;
  char *bytes = pts_table(data, properties, &len);
  write_line(stdout, bytes, len);
  free(bytes);
}
