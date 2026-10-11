/* Exceptions and the native error classes (error.c), and how console.log shows errors (inspect.c). */
#include "pts.h"

#include "check.h"

#include <stdio.h>
#include <stdlib.h>

char *pts_inspect(pts_value value, double depth, bool show_hidden, size_t *len); /* inspect.c */

static char *inspected(const void *object) {
  size_t len;
  return pts_inspect(pts_value_from_object((pts_object *)object), 2, false, &len);
}

static char *c_string(const pts_string *string) {
  char *copy = malloc(string->byte_len + 1);
  memcpy(copy, string->bytes, string->byte_len);
  copy[string->byte_len] = '\0';
  return copy;
}

static pts_error *new_error(const pts_type *type, const char *message) {
  return pts_error_new(type, pts_string_from_cstr(message));
}

static void throw_twice_removed(pts_value exception) { pts_throw(exception); }

static void a_throw_in_a_callee_lands_in_the_innermost_handler(void) {
  pts_handler outer, inner;
  volatile int landed = 0;
  pts_try_enter(&outer);
  if (_setjmp(outer.jump) == 0) {
    pts_try_enter(&inner);
    if (_setjmp(inner.jump) == 0) {
      throw_twice_removed(pts_value_from_number(42));
    } else {
      CHECK(pts_caught(&inner) == pts_value_from_number(42));
      CHECK(pts_handlers == &outer);
      landed = 1;
    }
    pts_try_leave(&outer);
  } else {
    landed = 2;
  }
  CHECK(landed == 1);
  CHECK(pts_handlers == NULL);
}

static void runtime_errors_are_catchable_errors(void) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_i32_div(1, 0);
    CHECK(false);
  } else {
    pts_value caught = pts_caught(&handler);
    CHECK(pts_value_instance_of(caught, &pts_type_builtin_RangeError));
    CHECK(pts_value_instance_of(caught, &pts_type_builtin_Error));
    char *text = c_string(pts_value_to_string(caught));
    CHECK_STR(text, "RangeError: Division by zero");
    free(text);
  }
  CHECK(pts_handlers == NULL);
}

static void to_string_leaves_out_an_empty_name_or_message(void) {
  pts_error *error = new_error(&pts_type_builtin_TypeError, "");
  char *text = c_string(pts_error_to_string(error));
  CHECK_STR(text, "TypeError");
  free(text);
  error->name = pts_string_from_cstr("");
  error->message = pts_string_from_cstr("lost");
  text = c_string(pts_error_to_string(error));
  CHECK_STR(text, "lost");
  free(text);
}

static void the_stack_is_formatted_from_the_name_when_first_read(void) {
  pts_error *error = new_error(&pts_type_builtin_Error, "boom");
  error->name = pts_string_from_cstr("Custom");
  char *text = c_string(pts_value_to_string(pts_error_get_stack(error)));
  CHECK_STR(text, "Custom: boom");
  free(text);
  error->name = pts_string_from_cstr("Later");
  text = c_string(pts_value_to_string(pts_error_get_stack(error)));
  CHECK_STR(text, "Custom: boom");
  free(text);
}

static void errors_print_as_node_prints_them_without_frames(void) {
  pts_error *error = new_error(&pts_type_builtin_Error, "w");
  char *text = inspected(error);
  CHECK_STR(text, "[Error: w]");
  free(text);
  pts_error *with_cause = new_error(&pts_type_builtin_RangeError, "c");
  with_cause->cause = pts_value_from_object(error);
  text = inspected(with_cause);
  CHECK_STR(text, "[RangeError: c] { [cause]: [Error: w] }");
  free(text);
  error->name = pts_string_from_cstr("Renamed");
  pts_error_set_stack(error, pts_value_from_string(pts_string_from_cstr("Error: w")));
  text = inspected(error);
  CHECK_STR(text, "[Error: w] { name: 'Renamed' }");
  free(text);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  a_throw_in_a_callee_lands_in_the_innermost_handler();
  runtime_errors_are_catchable_errors();
  to_string_leaves_out_an_empty_name_or_message();
  the_stack_is_formatted_from_the_name_when_first_read();
  errors_print_as_node_prints_them_without_frames();
  return pts_runtime_exit(CHECK_DONE());
}
