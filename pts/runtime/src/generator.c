#include "pts.h"

#include "dynamic_internal.h"

/*
 * Generators (D-109): generated code compiles a generator function's body to a step
 * function over a frame that starts with a pts_iterator; this file resumes it as JS's
 * next(), throw() and return() do.
 */

const pts_type pts_type_builtin_Generator = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS, /* frames are allocated with GC_MALLOC and scanned whole */
    .name = "Generator",
    .size = sizeof(pts_iterator),
};

pts_iterator *pts_generator_new(size_t size, bool (*step)(pts_iterator *)) {
  pts_iterator *generator = GC_MALLOC(size);
  if (generator == NULL) {
    pts_panic("RangeError", "Out of memory allocating a generator of %zu bytes", size);
  }
  generator->type = &pts_type_builtin_Generator;
  generator->step = step;
  generator->state = PTS_GENERATOR_START;
  generator->mode = PTS_RESUME_NEXT;
  generator->sent = PTS_VALUE_UNDEFINED;
  generator->value = PTS_VALUE_UNDEFINED;
  return generator;
}

static pts_iterator *as_iterator(pts_value value) {
  if (!pts_value_is_object(value) || pts_value_as_object(value)->type->kind != PTS_KIND_GENERATOR) {
    pts_throw_new(&pts_type_builtin_TypeError, "%s is not an iterator", pts_value_typeof(value)->bytes);
  }
  return (pts_iterator *)pts_value_as_object(value);
}

/* finish ends a generator as return(value) does, reporting it done. */
static bool finish(pts_iterator *generator, pts_value value) {
  generator->state = PTS_GENERATOR_DONE;
  generator->value = value;
  return true;
}

/*
 * A runtime iterator over the items of an iterable (pts_value_iterable): an array, read
 * live, whose index is the iterator's state; PTS_GENERATOR_DONE once it has finished.
 */
typedef struct items_iterator {
  pts_iterator header;
  pts_value items;
} items_iterator;

static const pts_type array_iterator_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS, /* allocated with GC_MALLOC and scanned whole */
    .name = "Array Iterator",
    .size = sizeof(items_iterator),
};

static bool items_step(pts_iterator *it) {
  items_iterator *items = (items_iterator *)it;
  if (it->state < 0 || (size_t)it->state >= pts_spread_length(items->items)) {
    return finish(it, PTS_VALUE_UNDEFINED);
  }
  it->value = pts_spread_element(items->items, (size_t)it->state);
  it->state++;
  return false;
}

pts_value pts_iterator_open(pts_value iterable, const pts_string *text) {
  if (pts_value_is_object(iterable) && pts_value_as_object(iterable)->type->kind == PTS_KIND_GENERATOR) {
    return iterable;
  }
  items_iterator *it = GC_MALLOC(sizeof *it);
  if (it == NULL) {
    pts_panic("RangeError", "Out of memory allocating an iterator");
  }
  it->header.type = &array_iterator_type;
  it->header.step = items_step;
  it->header.value = PTS_VALUE_UNDEFINED;
  it->items = pts_value_iterable(iterable, text);
  return pts_value_from_object(it);
}

/* resume_runtime_iterator resumes an iterator of the runtime's, which has neither return() nor throw(). */
static bool resume_runtime_iterator(pts_iterator *it, pts_resume_mode mode, pts_value sent) {
  switch (mode) {
  case PTS_RESUME_NEXT:
    return it->step(it);
  case PTS_RESUME_RETURN:
    return finish(it, sent);
  case PTS_RESUME_THROW:
    finish(it, PTS_VALUE_UNDEFINED);
    pts_throw_new(&pts_type_builtin_TypeError, "The iterator does not provide a 'throw' method");
  }
  return true;
}

bool pts_iterator_resume(pts_value iterator, pts_resume_mode mode, pts_value sent) {
  pts_iterator *generator = as_iterator(iterator);
  if (generator->type != &pts_type_builtin_Generator) {
    return resume_runtime_iterator(generator, mode, sent);
  }
  switch (generator->state) {
  case PTS_GENERATOR_RUNNING:
    pts_throw_new(&pts_type_builtin_TypeError, "Generator is already running");
  case PTS_GENERATOR_START:
    if (mode == PTS_RESUME_NEXT) {
      break;
    }
    /* fall through: a generator that hasn't started finishes without running */
  case PTS_GENERATOR_DONE:
    switch (mode) {
    case PTS_RESUME_NEXT:
      return finish(generator, PTS_VALUE_UNDEFINED);
    case PTS_RESUME_RETURN:
      return finish(generator, sent);
    case PTS_RESUME_THROW:
      finish(generator, PTS_VALUE_UNDEFINED);
      pts_throw(sent);
    }
  }
  generator->mode = mode;
  generator->sent = sent;
  /* A marker on the chain of handlers: a throw out of the body finishes the generator. */
  pts_handler marker;
  marker.prev = pts_handlers;
  marker.generator = generator;
  pts_handlers = &marker;
  bool done = generator->step(generator);
  pts_handlers = marker.prev;
  return done;
}

pts_value pts_iterator_value(pts_value iterator) { return as_iterator(iterator)->value; }

void pts_iterator_close(pts_value iterator) {
  pts_iterator *generator = as_iterator(iterator);
  if (generator->type == &pts_type_builtin_Generator && generator->state != PTS_GENERATOR_DONE) {
    pts_iterator_resume(iterator, PTS_RESUME_RETURN, PTS_VALUE_UNDEFINED);
  }
}

pts_array *pts_iterator_to_array(pts_value iterator) {
  pts_array *values = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  while (!pts_iterator_resume(iterator, PTS_RESUME_NEXT, PTS_VALUE_UNDEFINED)) {
    pts_value value = pts_iterator_value(iterator);
    pts_array_push(values, &value, 1);
  }
  return values;
}

pts_array *pts_iterator_take(pts_value iterator, double count, bool rest) {
  if (rest) {
    return pts_iterator_to_array(iterator);
  }
  pts_array *values = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  for (double i = 0; i < count; i++) {
    if (pts_iterator_resume(iterator, PTS_RESUME_NEXT, PTS_VALUE_UNDEFINED)) {
      return values;
    }
    pts_value value = pts_iterator_value(iterator);
    pts_array_push(values, &value, 1);
  }
  pts_iterator_close(iterator);
  return values;
}
