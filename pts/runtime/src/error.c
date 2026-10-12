/*
 * Exceptions and the native error classes (D-068): the handler chain that `try` and
 * `throw` use, Error and its native subclasses, and what happens to an uncaught exception.
 */
#include "pts.h"

#include <backtrace.h>
#include <uv.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void pts_inspect_value_write(FILE *out, pts_value value); /* inspect.c */

_Thread_local pts_handler *pts_handlers;

/* An uncaught exception: its stack, or for any other value, the value as console.log shows it nested (D-O07). */
_Noreturn void pts_report_uncaught(pts_value exception) {
  pts_begin_exit();
  fflush(stdout);
  fputs("Uncaught ", stderr);
  if (pts_value_is_object(exception) && pts_is_error(pts_value_as_object(exception))) {
    pts_error *error = (pts_error *)pts_value_as_object(exception);
    pts_string_write_for_output(pts_value_to_string(pts_error_get_stack(error)), stderr);
  } else {
    pts_inspect_value_write(stderr, exception);
  }
  fputc('\n', stderr);
  exit(1);
}

void pts_throw(pts_value exception) {
  pts_handler *handler = pts_handlers;
  while (handler != NULL && handler->generator != NULL) { /* a generator the throw leaves is finished (D-109) and released (D-143) */
    handler->generator->state = PTS_GENERATOR_DONE;
    handler->generator->value = PTS_VALUE_UNDEFINED;
    __atomic_store_n(&handler->generator->executing, false, __ATOMIC_RELEASE);
    handler = handler->prev;
  }
  if (handler == NULL) {
    pts_report_uncaught(exception);
  }
  pts_handlers = handler->prev;
  handler->exception = exception;
  _longjmp(handler->jump, 1);
}

/* ---------------------------------------------------------------- the native error classes */

/*
 * The fields interfaces and console.log see; the stack is a getter and setter, which
 * also makes `"stack" in e` true, as in JS.
 */
static const pts_field error_fields[] = {
    {"name", offsetof(pts_error, name), PTS_FIELD_STRING},
    {"message", offsetof(pts_error, message), PTS_FIELD_STRING},
    {"cause", offsetof(pts_error, cause), PTS_FIELD_VALUE},
};

static pts_value stack_getter(pts_object *error) { return pts_error_get_stack((pts_error *)error); }
static void stack_setter(pts_object *error, pts_value stack) { pts_error_set_stack((pts_error *)error, stack); }
static pts_string *to_string_method(pts_object *error) { return pts_error_to_string((pts_error *)error); }
static pts_value to_string_dynamic(pts_object *error, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return pts_value_from_string(pts_error_to_string((pts_error *)error));
}

static const pts_method error_methods[] = {
    {"stack", PTS_GETTER, (pts_function)stack_getter, PTS_FIELD_VALUE},
    {"stack", PTS_SETTER, (pts_function)stack_setter, PTS_FIELD_VALUE},
    {"toString", PTS_METHOD, (pts_function)to_string_method, PTS_FIELD_STRING, NULL, to_string_dynamic},
};

#define ERROR_LAYOUT                                                                                   \
  PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_error, name) | PTS_GC_WORD(pts_error, message) |               \
                       PTS_GC_WORD(pts_error, cause) | PTS_GC_WORD(pts_error, pts_private_stack) |    \
                       PTS_GC_WORD(pts_error, pts_private_frames))

#define NATIVE_ERROR(class_name, parent_type)                                                          \
  const pts_type pts_type_builtin_##class_name = {                                                     \
      .kind = PTS_KIND_OBJECT,                                                                         \
      .gc_descr = ERROR_LAYOUT,                                                                        \
      .name = #class_name,                                                                             \
      .parent = (parent_type),                                                                         \
      .size = sizeof(pts_error),                                                                       \
      .field_count = sizeof error_fields / sizeof error_fields[0],                                     \
      .fields = error_fields,                                                                          \
      .method_count = sizeof error_methods / sizeof error_methods[0],                                  \
      .methods = error_methods,                                                                        \
  }

NATIVE_ERROR(Error, NULL);
NATIVE_ERROR(TypeError, &pts_type_builtin_Error);
NATIVE_ERROR(RangeError, &pts_type_builtin_Error);
NATIVE_ERROR(ReferenceError, &pts_type_builtin_Error);
NATIVE_ERROR(SyntaxError, &pts_type_builtin_Error);
NATIVE_ERROR(EvalError, &pts_type_builtin_Error);
NATIVE_ERROR(URIError, &pts_type_builtin_Error);

/* AggregateError (D-113): an Error with the errors it aggregates, which Promise.any rejects with. */
static const pts_field aggregate_error_fields[] = {
    {"name", offsetof(pts_error, name), PTS_FIELD_STRING},
    {"message", offsetof(pts_error, message), PTS_FIELD_STRING},
    {"cause", offsetof(pts_error, cause), PTS_FIELD_VALUE},
    {"errors", offsetof(pts_aggregate_error, errors), PTS_FIELD_OBJECT},
};

const pts_type pts_type_builtin_AggregateError = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = ERROR_LAYOUT | PTS_GC_WORD(pts_aggregate_error, errors),
    .name = "AggregateError",
    .parent = &pts_type_builtin_Error,
    .size = sizeof(pts_aggregate_error),
    .field_count = sizeof aggregate_error_fields / sizeof aggregate_error_fields[0],
    .fields = aggregate_error_fields,
    .method_count = sizeof error_methods / sizeof error_methods[0],
    .methods = error_methods,
};

/*
 * Node's errors of system calls (D-117): Errors (the class is named "Error", as their
 * constructor is in Node) with the call's errno, code, syscall, path and dest, which
 * console.log shows after the stack; path and dest are absent if the call had none.
 */
static const pts_field system_error_fields[] = {
    {"name", offsetof(pts_error, name), PTS_FIELD_STRING},
    {"message", offsetof(pts_error, message), PTS_FIELD_STRING},
    {"cause", offsetof(pts_error, cause), PTS_FIELD_VALUE},
    {"errno", offsetof(pts_system_error, errno_value), PTS_FIELD_VALUE},
    {"code", offsetof(pts_system_error, code), PTS_FIELD_VALUE},
    {"syscall", offsetof(pts_system_error, syscall), PTS_FIELD_VALUE},
    {"path", offsetof(pts_system_error, path), PTS_FIELD_VALUE},
    {"dest", offsetof(pts_system_error, dest), PTS_FIELD_VALUE},
};

const pts_type pts_type_builtin_SystemError = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = ERROR_LAYOUT | PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_system_error, errno_value) | PTS_GC_WORD(pts_system_error, code) |
                                                    PTS_GC_WORD(pts_system_error, syscall) | PTS_GC_WORD(pts_system_error, path) |
                                                    PTS_GC_WORD(pts_system_error, dest)),
    .name = "Error",
    .parent = &pts_type_builtin_Error,
    .size = sizeof(pts_system_error),
    .field_count = sizeof system_error_fields / sizeof system_error_fields[0],
    .fields = system_error_fields,
    .method_count = sizeof error_methods / sizeof error_methods[0],
    .methods = error_methods,
};

static const pts_type *const native_errors[] = {
    &pts_type_builtin_Error,       &pts_type_builtin_TypeError, &pts_type_builtin_RangeError,     &pts_type_builtin_ReferenceError,
    &pts_type_builtin_SyntaxError, &pts_type_builtin_EvalError, &pts_type_builtin_URIError,       &pts_type_builtin_AggregateError,
    &pts_type_builtin_SystemError,
};
enum { NATIVE_ERROR_COUNT = sizeof native_errors / sizeof native_errors[0] };

/* The native error class type is or derives from, whose name its instances start with (JS: the prototype's name); NULL if none. */
static const pts_type *native_error_of(const pts_type *type) {
  for (; type != NULL; type = type->parent) {
    for (size_t i = 0; i < NATIVE_ERROR_COUNT; i++) {
      if (native_errors[i] == type) {
        return type;
      }
    }
  }
  return NULL;
}

bool pts_is_error(const pts_object *object) { return native_error_of(object->type) != NULL; }

PTS_STATIC_STRING(empty, PTS_STRING_ASCII, 0, 0, "");

/* ---------------------------------------------------------------- stack traces (D-069) */

/* V8's Error.stackTraceLimit; frames of the runtime come first and are dropped, so more are captured. */
enum { STACK_TRACE_LIMIT = 10, CAPTURED_FRAMES = 24 };

/* The return addresses where an error was made, innermost first; pointer-free. */
typedef struct frames {
  size_t count;
  uintptr_t pcs[CAPTURED_FRAMES];
} frames;

static const pts_function_name *function_names;
static size_t function_name_count;

void pts_register_function_names(const pts_function_name *names, size_t count) {
  function_names = names;
  function_name_count = count;
}

static int compare_c_names(const void *key, const void *entry) {
  return strcmp(key, ((const pts_function_name *)entry)->c_name);
}

static const pts_function_name *function_name_of(const char *c_name) {
  if (c_name == NULL || function_names == NULL) {
    return NULL;
  }
  return bsearch(c_name, function_names, function_name_count, sizeof *function_names, compare_c_names);
}

static struct backtrace_state *state;
static pthread_once_t state_once = PTHREAD_ONCE_INIT;

static void ignore_error(void *data, const char *message, int errnum) {
  (void)data, (void)message, (void)errnum; /* a frame without information is left out */
}

static void create_state(void) { state = backtrace_create_state(NULL, 1, ignore_error, NULL); }

static struct backtrace_state *shared_state(void) {
  pthread_once(&state_once, create_state);
  return state;
}

static int add_frame(void *data, uintptr_t pc) {
  frames *captured = data;
  captured->pcs[captured->count++] = pc;
  return captured->count == CAPTURED_FRAMES;
}

/* The return addresses of the calls that are making error, captured where it is made. */
static void *capture_frames(void) {
  frames *captured = GC_MALLOC_ATOMIC(sizeof *captured);
  if (captured == NULL) {
    return NULL; /* the error just has no frames */
  }
  captured->count = 0;
  backtrace_simple(shared_state(), 1, add_frame, ignore_error, captured);
  return captured;
}

/* A growable C string for formatting a stack. */
typedef struct trace {
  char *bytes;
  size_t len, cap;
  size_t shown;    /* frames written so far */
  bool any_source; /* whether a frame of the program's source was met, before which constructors of errors are left out */
} trace;

static void trace_add(trace *t, const char *text, size_t len) {
  if (t->len + len + 1 > t->cap) {
    size_t cap = (t->len + len + 1) * 2;
    char *bytes = realloc(t->bytes, cap);
    if (bytes == NULL) {
      pts_panic("RangeError", "Out of memory formatting a stack trace");
    }
    t->bytes = bytes;
    t->cap = cap;
  }
  memcpy(t->bytes + t->len, text, len);
  t->len += len;
  t->bytes[t->len] = '\0';
}

static bool ends_with(const char *text, const char *suffix) {
  size_t len = strlen(text), suffix_len = strlen(suffix);
  return len >= suffix_len && strcmp(text + len - suffix_len, suffix) == 0;
}

/*
 * One frame of a stack, as Node writes it: "    at name (file:line)", or for top-level
 * code and anonymous functions "    at file:line". Only frames in the program's source
 * count: the runtime's C, inlined headers and libc are left out.
 */
static int add_source_frame(void *data, uintptr_t pc, const char *file, int line, const char *function) {
  (void)pc;
  trace *t = data;
  if (file == NULL || line == 0 || ends_with(file, ".c") || ends_with(file, ".h") || t->shown == STACK_TRACE_LIMIT) {
    return 0;
  }
  const pts_function_name *name = function_name_of(function);
  if (!t->any_source && name != NULL && name->error_constructor) {
    return 0; /* `new MyError(…)` itself, as V8 leaves it out */
  }
  t->any_source = true;
  const char *module = strstr(file, "/pts:");
  if (module != NULL) {
    file = module + 1; /* a pts: module's name (D-116), which libbacktrace puts under the build's directory as a relative path */
  }
  char text[64];
  trace_add(t, "\n    at ", 8);
  if (name != NULL && name->js_name[0] != '\0') {
    trace_add(t, name->js_name, strlen(name->js_name));
    trace_add(t, " (", 2);
  }
  trace_add(t, file, strlen(file));
  int len = snprintf(text, sizeof text, ":%d%s", line, name != NULL && name->js_name[0] != '\0' ? ")" : "");
  trace_add(t, text, (size_t)len);
  t->shown++;
  return 0;
}

/* The stack of error: its toString(), then the source frames where it was made. */
static pts_string *format_stack(pts_error *error) {
  pts_string *header = pts_error_to_string(error);
  const frames *captured = error->pts_private_frames;
  if (captured == NULL) {
    return header;
  }
  trace t = {0};
  trace_add(&t, header->bytes, header->byte_len);
  for (size_t i = 0; i < captured->count; i++) {
    backtrace_pcinfo(shared_state(), captured->pcs[i], add_source_frame, ignore_error, &t);
  }
  pts_string *stack = pts_string_from_utf8(t.bytes, t.len);
  free(t.bytes);
  return stack;
}

void pts_error_init(pts_error *error, pts_string *message, bool message_given, pts_object *options, bool options_given) {
  error->name = pts_string_from_cstr(native_error_of(error->pts_header)->name);
  error->message = message_given ? message : empty;
  error->cause = PTS_VALUE_ABSENT;
  error->pts_private_stack = PTS_VALUE_ABSENT;
  error->pts_private_frames = capture_frames();
  if (options_given) {
    static pts_site cause_site;
    error->cause = pts_optional_get(options, &cause_site, "cause", NULL);
  }
}

void pts_aggregate_error_init(pts_aggregate_error *error, pts_value errors, pts_string *message, bool message_given, pts_object *options,
                              bool options_given) {
  PTS_STATIC_STRING(text, PTS_STRING_ASCII, 6, 6, "errors");
  pts_error_init((pts_error *)error, message, message_given, options, options_given);
  error->errors = pts_value_to_array(errors, text);
}

pts_value pts_aggregate_error_new(pts_value errors, pts_string *message) {
  pts_aggregate_error *error = pts_object_new(&pts_type_builtin_AggregateError);
  pts_aggregate_error_init(error, errors, message, true, NULL, false);
  return pts_value_from_object(error);
}

pts_value pts_system_error_new(int error, pts_string *message, const char *syscall, pts_string *path, pts_string *dest) {
  pts_system_error *system_error = pts_object_new(&pts_type_builtin_SystemError);
  pts_error_init((pts_error *)system_error, message, true, NULL, false);
  system_error->errno_value = pts_value_from_number(error);
  system_error->code = pts_value_from_string(pts_string_from_cstr(uv_err_name(error)));
  system_error->syscall = pts_value_from_string(pts_string_from_cstr(syscall));
  system_error->path = path != NULL && path->byte_len > 0 ? pts_value_from_string(path) : PTS_VALUE_ABSENT;
  system_error->dest = dest != NULL && dest->byte_len > 0 ? pts_value_from_string(dest) : PTS_VALUE_ABSENT;
  return pts_value_from_object(system_error);
}

/* Node's uvException: "CODE: description, syscall 'path' -> 'dest'", without the parts the call hasn't. */
pts_value pts_uv_error_new(int error, const char *syscall, pts_string *path, pts_string *dest) {
  trace message = {0};
  const char *parts[] = {uv_err_name(error), ": ", uv_strerror(error), ", ", syscall};
  for (size_t i = 0; i < sizeof parts / sizeof parts[0]; i++) {
    trace_add(&message, parts[i], strlen(parts[i]));
  }
  if (path != NULL && path->byte_len > 0) {
    trace_add(&message, " '", 2);
    trace_add(&message, path->bytes, path->byte_len);
    trace_add(&message, "'", 1);
  }
  if (dest != NULL && dest->byte_len > 0) {
    trace_add(&message, " -> '", 5);
    trace_add(&message, dest->bytes, dest->byte_len);
    trace_add(&message, "'", 1);
  }
  pts_string *text = pts_string_from_utf8(message.bytes, message.len);
  free(message.bytes);
  return pts_system_error_new(error, text, syscall, path, dest);
}

_Noreturn void pts_throw_uv_error(int error, const char *syscall, pts_string *path, pts_string *dest) {
  pts_throw(pts_uv_error_new(error, syscall, path, dest));
}

pts_error *pts_error_new(const pts_type *type, pts_string *message) {
  pts_error *error = pts_object_new(type);
  pts_error_init(error, message, true, NULL, false);
  return error;
}

PTS_STATIC_STRING(separator, PTS_STRING_ASCII, 2, 2, ": ");

pts_string *pts_error_to_string(pts_error *error) {
  if (error->name->byte_len == 0) {
    return error->message;
  }
  if (error->message->byte_len == 0) {
    return error->name;
  }
  return pts_string_concat(pts_string_concat(error->name, separator), error->message);
}

pts_value pts_error_get_stack(pts_error *error) {
  if (error->pts_private_stack == PTS_VALUE_ABSENT) {
    error->pts_private_stack = pts_value_from_string(format_stack(error));
  }
  return error->pts_private_stack;
}

void pts_error_set_stack(pts_error *error, pts_value stack) { error->pts_private_stack = stack; }

void pts_throw_new(const pts_type *type, const char *format, ...) {
  va_list args;
  va_start(args, format);
  char small[256];
  int length = vsnprintf(small, sizeof small, format, args);
  va_end(args);
  pts_string *message;
  if (length < 0) {
    message = pts_string_from_cstr(format);
  } else if ((size_t)length < sizeof small) {
    message = pts_string_from_utf8(small, (size_t)length);
  } else {
    char *large = malloc((size_t)length + 1);
    if (large == NULL) {
      pts_panic("RangeError", "Out of memory formatting an error message");
    }
    va_start(args, format);
    vsnprintf(large, (size_t)length + 1, format, args);
    va_end(args);
    message = pts_string_from_utf8(large, (size_t)length);
    free(large);
  }
  pts_throw(pts_value_from_object(pts_error_new(type, message)));
}

void pts_throw_error(pts_string *name, pts_string *message) {
  const pts_type *type = &pts_type_builtin_Error;
  for (size_t i = 0; i < NATIVE_ERROR_COUNT; i++) {
    if (name->byte_len == strlen(native_errors[i]->name) && memcmp(name->bytes, native_errors[i]->name, name->byte_len) == 0) {
      type = native_errors[i];
    }
  }
  pts_throw(pts_value_from_object(pts_error_new(type, message)));
}
