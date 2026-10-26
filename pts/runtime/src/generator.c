#include "pts.h"

#include "dynamic_internal.h"
#include "scheduler_internal.h"

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
  generator->result = PTS_VALUE_ABSENT;
  return generator;
}

static pts_iterator *as_iterator(pts_value value) {
  if (!pts_value_is_object(value) || pts_value_as_object(value)->type->kind != PTS_KIND_GENERATOR) {
    pts_throw_new(&pts_type_builtin_TypeError, "%s is not an iterator", pts_value_typeof(value)->bytes);
  }
  return (pts_iterator *)pts_value_as_object(value);
}

/*
 * The value and result object this thread's last resume of an iterator gave, once threads
 * run the program's code. Threads may share an iterator, and another thread's resume may
 * replace them before this thread reads them, so pts_iterator_value reads them from here
 * (D-143). Uncollectable, so the collector sees it.
 */
typedef struct resumed {
  const pts_iterator *iterator;
  pts_value value;
  pts_value result;
} resumed;

static _Thread_local resumed *last_resumed;

static void remember_step(const pts_iterator *it, pts_value value, pts_value result) {
  if (last_resumed == NULL) {
    last_resumed = GC_MALLOC_UNCOLLECTABLE(sizeof *last_resumed);
    if (last_resumed == NULL) {
      pts_panic("RangeError", "Out of memory allocating an iterator's value");
    }
    PTS_PUBLICATION_PRIVATE(last_resumed);
  }
  last_resumed->iterator = it;
  last_resumed->value = value;
  last_resumed->result = result;
}

/* Sets what an iterator's step gave, as this thread's resume of it gave it: its value, or a result object whose value is read if asked for. */
static void set_step(pts_iterator *it, pts_value value, pts_value result) {
  it->value = value;
  it->result = result;
  if (__atomic_load_n(&pts_threaded, __ATOMIC_RELAXED)) {
    remember_step(it, value, result);
  }
}

static void set_value(pts_iterator *it, pts_value value) { set_step(it, value, PTS_VALUE_ABSENT); }

/* What this thread's last resume of an iterator gave: its value, and the result object it handed back or PTS_VALUE_ABSENT. */
static void last_step(const pts_iterator *it, pts_value *value, pts_value *result) {
  *value = it->value;
  *result = it->result;
  if (__atomic_load_n(&pts_threaded, __ATOMIC_RELAXED) && last_resumed != NULL && last_resumed->iterator == it) {
    *value = last_resumed->value;
    *result = last_resumed->result;
  }
}

/* finish ends a generator as return(value) does, reporting it done. */
static bool finish(pts_iterator *generator, pts_value value) {
  generator->state = PTS_GENERATOR_DONE;
  set_value(generator, value);
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
  /* One load of the position: threads sharing the iterator may move it meanwhile, and a tuple's elements aren't bounds-checked (D-143). */
  int32_t position = __atomic_load_n(&it->state, __ATOMIC_RELAXED);
  if (position < 0 || (size_t)position >= pts_spread_length(items->items)) {
    return finish(it, PTS_VALUE_UNDEFINED);
  }
  set_value(it, pts_spread_element(items->items, (size_t)position));
  __atomic_store_n(&it->state, position + 1, __ATOMIC_RELAXED);
  return false;
}

static const pts_type string_iterator_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "String Iterator",
    .size = sizeof(items_iterator),
};

static pts_value items_iterator_new(const pts_type *type, pts_value items) {
  items_iterator *it = GC_MALLOC(sizeof *it);
  if (it == NULL) {
    pts_panic("RangeError", "Out of memory allocating an iterator");
  }
  it->header.type = type;
  it->header.step = items_step;
  it->header.value = PTS_VALUE_UNDEFINED;
  it->header.result = PTS_VALUE_ABSENT;
  it->items = items;
  pts_publish();
  return pts_value_from_object(it);
}

pts_value pts_array_iterator_new(pts_value items) { return items_iterator_new(&array_iterator_type, items); }

static bool is_iterator(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_GENERATOR;
}

/*
 * The iteration protocol over the program's objects (D-111): an object with a method
 * `@@iterator` ([Symbol.iterator]) is iterable, and an object with a next() is an
 * iterator, which a runtime iterator wraps (object_iterator): its steps call next(),
 * return() and throw() through their dynamic entries (D-074) and read done off the
 * results, which they keep (pts_iterator's result): a result's value is read only if it
 * is asked for, as JS's IteratorStep leaves it for IteratorValue (an elision or a done
 * for…of never reads it, D-146, H03b).
 */
typedef struct object_iterator {
  pts_iterator header;
  pts_value object;
} object_iterator;

static const pts_type object_iterator_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS, /* allocated with GC_MALLOC and scanned whole */
    .name = "Object Iterator",
    .size = sizeof(object_iterator),
};

/* Whether object is one of the program's objects with a method name, or a function in its property name. */
static bool has_method(pts_value object, const pts_string *name) {
  if (!pts_value_is_object(object) || pts_value_as_object(object)->type->kind != PTS_KIND_OBJECT) {
    return false;
  }
  pts_value function = pts_dynamic_get(object, pts_value_from_string(name));
  return pts_value_is_object(function) && pts_value_as_object(function)->type->kind == PTS_KIND_FUNCTION;
}

/* object.name(argv…), a method of the program's object, through its dynamic entry; text names it in the TypeError if it has none. */
static pts_value call_method(pts_value object, const pts_string *name, const pts_string *text, uint32_t argc, const pts_value *argv) {
  return pts_dynamic_call_method(object, pts_value_from_string(name), text, argc, argv);
}

PTS_STATIC_STRING(iterator_key, 1, 10, 10, "@@iterator");

pts_value pts_iterator_method(pts_value value) {
  if (!pts_value_is_object(value) || pts_value_as_object(value)->type->kind != PTS_KIND_OBJECT) {
    return PTS_VALUE_UNDEFINED;
  }
  return pts_dynamic_get(value, pts_value_from_string(iterator_key));
}

/*
 * What an object's [Symbol.iterator] method, as pts_iterator_method read it, returns; a
 * TypeError `text is not iterable` if it is no function (null too, as GetIterator has it).
 */
static pts_value call_iterator_method(pts_value method, const pts_string *text) {
  if (!pts_value_is_object(method) || pts_value_as_object(method)->type->kind != PTS_KIND_FUNCTION) {
    pts_throw_new(&pts_type_builtin_TypeError, "%.*s is not iterable", (int)text->byte_len, text->bytes);
  }
  return pts_dynamic_call(method, iterator_key, 0, NULL);
}

pts_value pts_iterator_of(pts_value iterator) {
  if (is_iterator(iterator)) {
    return iterator;
  }
  if (!pts_value_is_object(iterator) || pts_value_as_object(iterator)->type->kind != PTS_KIND_OBJECT) {
    pts_throw_new(&pts_type_builtin_TypeError, "Result of the Symbol.iterator method is not an object");
  }
  object_iterator *it = GC_MALLOC(sizeof *it);
  if (it == NULL) {
    pts_panic("RangeError", "Out of memory allocating an iterator");
  }
  it->header.type = &object_iterator_type;
  it->header.value = PTS_VALUE_UNDEFINED;
  it->header.result = PTS_VALUE_ABSENT;
  it->object = iterator;
  pts_publish();
  return pts_value_from_object(it);
}

/* The iterator method that resumes in mode: next, throw or return. */
static const pts_string *method_name(pts_resume_mode mode) {
  PTS_STATIC_STRING(next, 1, 4, 4, "next");
  PTS_STATIC_STRING(throw_, 1, 5, 5, "throw");
  PTS_STATIC_STRING(return_, 1, 6, 6, "return");
  return mode == PTS_RESUME_NEXT ? next : mode == PTS_RESUME_THROW ? throw_ : return_;
}

/*
 * Calls an object's next(sent), throw(sent) or return(sent): the result object, kept as
 * the iterator's step with its value unread. text names the method in the TypeError of an
 * object without it.
 */
static inline __attribute__((always_inline)) pts_value call_object_method(object_iterator *it, pts_resume_mode mode, pts_value sent, const pts_string *text) {
  pts_value result = call_method(it->object, method_name(mode), text, 1, &sent);
  if (!pts_value_is_object(result)) {
    pts_string *shown = pts_value_to_string(result);
    pts_throw_new(&pts_type_builtin_TypeError, "Iterator result %.*s is not an object", (int)shown->byte_len, shown->bytes);
  }
  set_step(&it->header, PTS_VALUE_ABSENT, result);
  return result;
}

/*
 * call_object_method as yield* and IteratorClose call it: for a missing return(),
 * PTS_VALUE_TRUE, the iterator finished with sent, as yield* does; for a missing throw(),
 * the iterator closed and a TypeError.
 */
static pts_value call_object_iterator(object_iterator *it, pts_resume_mode mode, pts_value sent) {
  const pts_string *name = method_name(mode);
  if (mode != PTS_RESUME_NEXT && !has_method(it->object, name)) {
    if (mode == PTS_RESUME_RETURN) {
      finish(&it->header, sent);
      return PTS_VALUE_TRUE;
    }
    pts_iterator_close(pts_value_from_object(it));
    pts_throw_new(&pts_type_builtin_TypeError, "The iterator does not provide a 'throw' method.");
  }
  return call_object_method(it, mode, sent, name);
}

/* Whether a result object says it is done: JS's IteratorComplete. */
static bool result_done(pts_value result) {
  PTS_STATIC_STRING(done, 1, 4, 4, "done");
  return pts_value_truthy(pts_dynamic_get(result, pts_value_from_string(done)));
}

/* resume_object_iterator resumes an object's iterator by calling its next(), throw() or return() as yield* does, reading its result's done (IteratorComplete). */
static bool resume_object_iterator(object_iterator *it, pts_resume_mode mode, pts_value sent) {
  pts_value result = call_object_iterator(it, mode, sent);
  if (!pts_value_is_object(result)) {
    return true; /* finished without a return() */
  }
  if (!result_done(result)) {
    return false;
  }
  it->header.state = PTS_GENERATOR_DONE;
  return true;
}

/* Whether value is a map or a set, whose iterators are live (pts_map_iterator_new). */
static bool is_collection(pts_value value) {
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_kind kind = pts_value_as_object(value)->type->kind;
  return kind == PTS_KIND_MAP || kind == PTS_KIND_SET;
}

/* An iterator over a map's entries or a set's elements, as their [Symbol.iterator]() makes. */
static pts_value collection_iterator(pts_value collection) {
  return pts_map_iterator_new(collection, pts_value_as_object(collection)->type->kind == PTS_KIND_SET ? 0 : 2);
}

pts_value pts_iterator_get(pts_value iterable, const pts_string *text) {
  if (is_iterator(iterable)) {
    return iterable; /* a generator's or runtime iterator's [Symbol.iterator]() is itself */
  }
  pts_value method = pts_iterator_method(iterable);
  if (method != PTS_VALUE_UNDEFINED) {
    return call_iterator_method(method, text);
  }
  if (pts_value_is_object(iterable)) {
    switch (pts_value_as_object(iterable)->type->kind) {
    case PTS_KIND_MAP:
    case PTS_KIND_SET:
      return collection_iterator(iterable);
    case PTS_KIND_STRING:
      return items_iterator_new(&string_iterator_type, pts_value_iterable(iterable, text));
    default:
      break;
    }
  }
  return items_iterator_new(&array_iterator_type, pts_value_iterable(iterable, text));
}

pts_value pts_iterator_open(pts_value iterable, const pts_string *text) {
  if (is_iterator(iterable)) {
    return iterable;
  }
  pts_value method = pts_iterator_method(iterable);
  if (method != PTS_VALUE_UNDEFINED) {
    return pts_iterator_of(call_iterator_method(method, text));
  }
  if (is_collection(iterable)) {
    return collection_iterator(iterable);
  }
  return items_iterator_new(&array_iterator_type, pts_value_iterable(iterable, text));
}

pts_value pts_iteration_open(pts_value iterable, const pts_string *text) {
  if (is_iterator(iterable) || is_collection(iterable)) {
    return pts_iterator_open(iterable, text); /* stepped by next(), lazily, and a map's or set's live */
  }
  pts_value method = pts_iterator_method(iterable);
  if (method != PTS_VALUE_UNDEFINED) {
    return pts_iterator_of(call_iterator_method(method, text)); /* stepped by next(), lazily */
  }
  if (pts_value_is_object(iterable) && pts_value_as_object(iterable)->type->kind == PTS_KIND_TYPED_ARRAY) {
    return iterable; /* read live, as an array is */
  }
  return pts_value_iterable(iterable, text);
}

bool pts_iteration_next(pts_value source, double index) {
  if (is_iterator(source)) {
    return pts_iterator_resume(source, PTS_RESUME_NEXT, PTS_VALUE_UNDEFINED);
  }
  return index >= (double)pts_spread_length(source);
}

pts_value pts_iteration_value(pts_value source, double index) {
  if (is_iterator(source)) {
    return pts_iterator_value(source);
  }
  return pts_spread_element(source, (size_t)index);
}

pts_array *pts_iteration_rest(pts_value source, double index, bool done) {
  if (is_iterator(source) && !done) {
    return pts_iterator_to_array(source);
  }
  pts_array *rest = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  for (size_t i = (size_t)index, n = done ? 0 : pts_spread_length(source); i < n; i++) {
    pts_value item = pts_spread_element(source, i);
    pts_array_push(rest, &item, 1);
  }
  pts_publish();
  return rest;
}

void pts_iteration_close(pts_value source) {
  if (is_iterator(source)) {
    pts_iterator_close(source);
  }
}

/* A runtime iterator over a map or set (pts_map_iterator_new). */
typedef struct map_iterator {
  pts_iterator header;
  pts_map_iterator entries;
  int32_t items; /* 0: values, 1: keys, 2: entries */
} map_iterator;

static const pts_type map_iterator_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS, /* allocated with GC_MALLOC and scanned whole */
    .name = "Map Iterator",
    .size = sizeof(map_iterator),
};

static const pts_type set_iterator_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Set Iterator",
    .size = sizeof(map_iterator),
};

/*
 * Moves a map iterator one entry on, in a private copy of its place: threads sharing the
 * iterator may move it meanwhile, so its table and position are each loaded once (every
 * index then stays inside the table it reads) and stored back, and the entry is this
 * step's own (D-143).
 */
static bool next_entry(map_iterator *iterator, pts_map_iterator *entry) {
  entry->map = iterator->entries.map;
  entry->table = __atomic_load_n(&iterator->entries.table, __ATOMIC_ACQUIRE);
  entry->position = __atomic_load_n(&iterator->entries.position, __ATOMIC_RELAXED);
  bool found = pts_map_next(entry);
  __atomic_store_n(&iterator->entries.table, entry->table, __ATOMIC_RELEASE);
  PTS_PUBLICATION_RELEASE(&iterator->entries.table, NULL);
  __atomic_store_n(&iterator->entries.position, entry->position, __ATOMIC_RELAXED);
  return found;
}

static bool map_step(pts_iterator *it) {
  map_iterator *iterator = (map_iterator *)it;
  pts_map_iterator entry;
  if (__atomic_load_n(&it->state, __ATOMIC_RELAXED) < 0 || !next_entry(iterator, &entry)) {
    return finish(it, PTS_VALUE_UNDEFINED);
  }
  pts_map *map = entry.map;
  pts_value key = pts_value_of_slot(map->type->key, &entry.key);
  pts_value value = map->type->kind == PTS_KIND_SET ? key : pts_value_of_slot(map->type->element, &entry.value);
  switch (iterator->items) {
  case 0:
    set_value(it, value);
    break;
  case 1:
    set_value(it, key);
    break;
  default: {
    pts_value pair[] = {key, value};
    set_value(it, pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_VALUE], pair, 2)));
  }
  }
  return false;
}

pts_value pts_map_iterator_new(pts_value map, int32_t items) {
  map_iterator *it = GC_MALLOC(sizeof *it);
  if (it == NULL) {
    pts_panic("RangeError", "Out of memory allocating an iterator");
  }
  pts_map *source = (pts_map *)pts_value_as_object(map);
  it->header.type = source->type->kind == PTS_KIND_SET ? &set_iterator_type : &map_iterator_type;
  it->header.step = map_step;
  it->header.value = PTS_VALUE_UNDEFINED;
  it->header.result = PTS_VALUE_ABSENT;
  it->entries = pts_map_iterate(source);
  it->items = items;
  pts_publish();
  return pts_value_from_object(it);
}

bool pts_map_iterator_remaining(const pts_object *object, pts_array **items, bool *entries) {
  if (object->type != &map_iterator_type && object->type != &set_iterator_type) {
    return false;
  }
  map_iterator copy = *(const map_iterator *)object;
  *items = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  *entries = copy.items == 2;
  while (!map_step(&copy.header)) {
    pts_array_push(*items, &copy.header.value, 1);
  }
  pts_publish();
  return true;
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
    pts_throw_new(&pts_type_builtin_TypeError, "The iterator does not provide a 'throw' method.");
  }
  return true;
}

/*
 * Resumes a generator that this thread has claimed (pts_iterator_resume) and releases it,
 * also when it throws; *handed is the result object its yield* handed back, read while it
 * is still this thread's, or PTS_VALUE_ABSENT.
 */
static inline __attribute__((always_inline)) bool resume_claimed_generator(pts_iterator *generator, pts_resume_mode mode, pts_value sent, pts_value *handed) {
  *handed = PTS_VALUE_ABSENT;
  switch (generator->state) {
  case PTS_GENERATOR_START:
    if (mode == PTS_RESUME_NEXT) {
      break;
    }
    /* fall through: a generator that hasn't started finishes without running */
  case PTS_GENERATOR_DONE:
    finish(generator, mode == PTS_RESUME_RETURN ? sent : PTS_VALUE_UNDEFINED);
    __atomic_store_n(&generator->executing, false, __ATOMIC_RELEASE);
    PTS_PUBLICATION_RELEASE(&generator->executing, NULL);
    if (mode == PTS_RESUME_THROW) {
      pts_throw(sent);
    }
    return true;
  }
  generator->mode = mode;
  generator->sent = sent;
  generator->result = PTS_VALUE_ABSENT; /* until a yield* hands one back */
  /* A marker on the chain of handlers: a throw out of the body finishes the generator and releases it. */
  pts_handler marker;
  marker.prev = pts_handlers;
  marker.generator = generator;
  pts_handlers = &marker;
  bool done = generator->step(generator);
  pts_handlers = marker.prev;
  *handed = generator->result;
  if (__atomic_load_n(&pts_threaded, __ATOMIC_RELAXED)) {
    remember_step(generator, generator->value, *handed);
  }
  __atomic_store_n(&generator->executing, false, __ATOMIC_RELEASE);
  PTS_PUBLICATION_RELEASE(&generator->executing, NULL);
  return done;
}

/*
 * Claims a generator for this thread, false if it is running: one thread at a time runs a
 * generator's body and owns its frame (D-143). The exchange is locked only once other
 * threads may run the program's code.
 */
static bool claim(pts_iterator *generator) {
  if (!__atomic_load_n(&pts_threaded, __ATOMIC_RELAXED)) {
    bool running = __atomic_load_n(&generator->executing, __ATOMIC_RELAXED);
    __atomic_store_n(&generator->executing, true, __ATOMIC_RELAXED);
    return !running;
  }
  return !__atomic_exchange_n(&generator->executing, true, __ATOMIC_ACQUIRE);
}

/* Resumes a generator object of the program's, as pts_iterator_resume does; *handed as resume_claimed_generator's. */
static inline __attribute__((always_inline)) bool resume_generator(pts_iterator *generator, pts_resume_mode mode, pts_value sent, pts_value *handed) {
  if (!claim(generator)) { /* resumed from its own body or while another thread runs it */
    pts_throw_new(&pts_type_builtin_TypeError, "Generator is already running");
  }
  return resume_claimed_generator(generator, mode, sent, handed);
}

bool pts_iterator_resume(pts_value iterator, pts_resume_mode mode, pts_value sent) {
  pts_iterator *generator = as_iterator(iterator);
  if (generator->type == &object_iterator_type) {
    return resume_object_iterator((object_iterator *)generator, mode, sent);
  }
  if (generator->type != &pts_type_builtin_Generator) {
    return resume_runtime_iterator(generator, mode, sent);
  }
  pts_value handed;
  if (resume_generator(generator, mode, sent, &handed)) {
    return true;
  }
  /* A result its yield* handed back is the one its caller steps by: IteratorComplete reads its done again. */
  return __builtin_expect(handed != PTS_VALUE_ABSENT, 0) && result_done(handed);
}

pts_value pts_iterator_call(pts_value iterator, pts_resume_mode mode, pts_value sent, const pts_string *text) {
  pts_iterator *it = as_iterator(iterator);
  if (it->type == &object_iterator_type) {
    return call_object_method((object_iterator *)it, mode, sent, text); /* `it.return is not a function` for one without it */
  }
  pts_value handed = PTS_VALUE_ABSENT;
  bool done;
  if (it->type == &pts_type_builtin_Generator) {
    done = resume_generator(it, mode, sent, &handed);
  } else if (mode == PTS_RESUME_NEXT) {
    done = resume_runtime_iterator(it, mode, sent);
  } else { /* the iterators of arrays, strings, maps and sets have only next(), as JS's */
    pts_throw_new(&pts_type_builtin_TypeError, "%.*s is not a function", (int)text->byte_len, text->bytes);
  }
  if (!done && handed != PTS_VALUE_ABSENT) {
    return handed;
  }
  return pts_value_from_bool(done);
}

void pts_generator_delegate(pts_iterator *generator, pts_value inner) {
  const pts_iterator *it = as_iterator(inner);
  pts_value value = it->value; /* last_step's value, read alone on the common path, where the step made its own result */
  if (__atomic_load_n(&pts_threaded, __ATOMIC_RELAXED) && last_resumed != NULL && last_resumed->iterator == it) {
    value = last_resumed->value;
  }
  generator->value = value; /* its result stays absent from the resume that runs this step */
  if (__builtin_expect(value == PTS_VALUE_ABSENT, 0)) {
    pts_value result;
    last_step(it, &value, &result);
    generator->result = result;
  }
}

/*
 * An iterator's value when its step left it absent: the value of the result object the
 * step handed back, read now that it is asked for (D-146). Threads that share the iterator
 * may pair one step's absent value with another's absent result (D-143), read as undefined.
 */
static __attribute__((noinline)) pts_value handed_value(const pts_iterator *it) {
  pts_value value, result;
  last_step(it, &value, &result);
  if (!pts_value_is_object(result)) {
    return value == PTS_VALUE_ABSENT ? PTS_VALUE_UNDEFINED : value;
  }
  PTS_STATIC_STRING(value_key, 1, 5, 5, "value");
  return pts_dynamic_get(result, pts_value_from_string(value_key));
}

pts_value pts_iterator_value(pts_value iterator) {
  const pts_iterator *it = as_iterator(iterator);
  pts_value value = it->value;
  if (__atomic_load_n(&pts_threaded, __ATOMIC_RELAXED) && last_resumed != NULL && last_resumed->iterator == it) {
    value = last_resumed->value;
  }
  return __builtin_expect(value != PTS_VALUE_ABSENT, 1) ? value : handed_value(it);
}

/*
 * The method name's mode, or -1 if the iterator has no such method: a generator, an async
 * generator and an object's iterator have next(), return() and throw(); the runtime's
 * iterators over arrays, strings, maps and sets have only next(); a for await's wrapper of
 * a sync iterator, which the program never sees, has none here.
 */
static int method_mode(const pts_iterator *it, const char *name) {
  bool all = it->type == &pts_type_builtin_Generator || it->type == &object_iterator_type || pts_value_is_async_generator(pts_value_from_object((pts_iterator *)it));
  if (!all && it->step == NULL) {
    return -1;
  }
  if (strcmp(name, "next") == 0) {
    return PTS_RESUME_NEXT;
  }
  if (all && strcmp(name, "return") == 0) {
    return PTS_RESUME_RETURN;
  }
  if (all && strcmp(name, "throw") == 0) {
    return PTS_RESUME_THROW;
  }
  return -1;
}

bool pts_iterator_has_method(pts_value iterator, const char *name) { return method_mode(as_iterator(iterator), name) >= 0; }

bool pts_iterator_method_call(pts_value iterator, const char *name, uint32_t argc, const pts_value *argv, pts_value *result) {
  pts_iterator *it = as_iterator(iterator);
  int mode = method_mode(it, name);
  if (mode < 0) {
    return false;
  }
  pts_value sent = argc > 0 ? argv[0] : PTS_VALUE_UNDEFINED;
  if (pts_value_is_async_generator(iterator)) {
    *result = pts_async_iterator_resume(iterator, (pts_resume_mode)mode, sent);
    return true;
  }
  pts_value step = pts_iterator_call(iterator, (pts_resume_mode)mode, sent, method_name(mode));
  *result = pts_value_is_object(step) ? step : pts_iterator_result_new(pts_iterator_value(iterator), step == PTS_VALUE_TRUE);
  return true;
}

void pts_iterator_close(pts_value iterator) {
  pts_iterator *generator = as_iterator(iterator);
  if (generator->state == PTS_GENERATOR_DONE) {
    return;
  }
  if (generator->type == &object_iterator_type) {
    /* IteratorClose: return() and a check that its result is an object, nothing of which is read. */
    call_object_iterator((object_iterator *)generator, PTS_RESUME_RETURN, PTS_VALUE_UNDEFINED);
    generator->state = PTS_GENERATOR_DONE;
  } else if (generator->type == &pts_type_builtin_Generator) {
    pts_value handed;
    resume_generator(generator, PTS_RESUME_RETURN, PTS_VALUE_UNDEFINED, &handed);
  }
}

pts_array *pts_iterator_to_array(pts_value iterator) {
  pts_array *values = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  while (!pts_iterator_resume(iterator, PTS_RESUME_NEXT, PTS_VALUE_UNDEFINED)) {
    pts_value value = pts_iterator_value(iterator);
    pts_array_push(values, &value, 1);
  }
  pts_publish();
  return values;
}

