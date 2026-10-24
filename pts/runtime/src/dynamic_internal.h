/* What dynamic.c shares with dynamic_methods.c (D-074, D-076). */
#ifndef PTS_DYNAMIC_INTERNAL_H
#define PTS_DYNAMIC_INTERNAL_H

#include "pts.h"

/* How messages name a value of the wrong type: by typeof, or an object by its class's name (Point, Array, Object). */
const char *pts_value_description(pts_value value);

/* How many elements an array or tuple spread into arguments has (TypeError for anything else), and element i of it. */
size_t pts_spread_length(pts_value value);
pts_value pts_spread_element(pts_value value, size_t i);

/* Calls the builtin method name of value, a string, number, boolean, array, tuple or function, if it has one: whether it did. */
bool pts_builtin_method_call(pts_value value, const char *name, uint32_t argc, const pts_value *argv, pts_value *result);
/* Whether value has the builtin method name that pts_builtin_method_call would call. */
bool pts_builtin_method_has(pts_value value, const char *name);
/* pts_builtin_method_has for a name held in a string (`in`'s key), compared where it is. */
bool pts_builtin_method_named(pts_value value, const pts_string *name);

#endif
