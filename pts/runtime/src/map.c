/*
 * Map and Set (D-046): an ordered hash table. Entries are appended in insertion order and
 * marked deleted in place; an open-addressing index (twice the entries' capacity, so it
 * always has empty slots) finds them by hash. Growing, compacting and clearing build a new
 * table and publish it, so threads racing on a map never read or write out of bounds
 * (D-L13); iterators follow the chain of tables (JS's live iteration).
 */
#include "pts.h"

#include <string.h>

enum { MIN_CAPACITY = 4 };

/* mix spreads the bits of a word over the whole word (splitmix64's finaliser). */
static uint64_t mix(uint64_t x) {
  x ^= x >> 30;
  x *= UINT64_C(0xbf58476d1ce4e5b9);
  x ^= x >> 27;
  x *= UINT64_C(0x94d049bb133111eb);
  return x ^ (x >> 31);
}

static uint64_t hash_bytes(const char *bytes, size_t len) {
  uint64_t h = UINT64_C(0xcbf29ce484222325); /* FNV-1a */
  for (size_t i = 0; i < len; i++) {
    h = (h ^ (unsigned char)bytes[i]) * UINT64_C(0x100000001b3);
  }
  return mix(h);
}

/* number_hash hashes a number so that SameValueZero-equal numbers hash alike: -0 as 0, every NaN alike. */
static uint64_t number_hash(double number) {
  uint64_t bits;
  if (number == 0) {
    number = 0;
  } else if (number != number) {
    number = __builtin_nan("");
  }
  memcpy(&bits, &number, sizeof bits);
  return mix(bits);
}

static uint64_t value_hash(pts_value value) {
  if (pts_value_is_number(value)) {
    return number_hash(pts_value_as_number(value));
  }
  if (pts_value_is_object(value)) {
    const pts_object *object = pts_value_as_object(value);
    switch (object->type->kind) {
    case PTS_KIND_STRING:
      return hash_bytes(((const pts_string *)object)->bytes, ((const pts_string *)object)->byte_len);
    case PTS_KIND_INT64:
      return mix((uint64_t)((const pts_boxed_int64 *)object)->value);
    case PTS_KIND_UINT64:
      return mix(((const pts_boxed_uint64 *)object)->value);
    default:
      break;
    }
  }
  return mix(value);
}

/* key_hash hashes a key of the given kind, held zero-extended in a word. Never PTS_MAP_DELETED. */
static uint64_t key_hash(pts_field_kind kind, uint64_t key) {
  uint64_t h;
  switch (kind) {
  case PTS_FIELD_NUMBER: {
    double number;
    memcpy(&number, &key, sizeof number);
    h = number_hash(number);
    break;
  }
  case PTS_FIELD_F32: {
    float f;
    memcpy(&f, &key, sizeof f);
    h = number_hash(f);
    break;
  }
  case PTS_FIELD_STRING: {
    const pts_string *string = (const pts_string *)(uintptr_t)key;
    h = hash_bytes(string->bytes, string->byte_len);
    break;
  }
  case PTS_FIELD_VALUE:
    h = value_hash(key);
    break;
  default: /* booleans, integers and objects (by identity) */
    h = mix(key);
  }
  return h == PTS_MAP_DELETED ? 0 : h;
}

static bool values_equal(pts_value a, pts_value b) {
  if (a == b) {
    return true;
  }
  if (pts_value_is_number(a) || pts_value_is_number(b)) {
    if (!pts_value_is_number(a) || !pts_value_is_number(b)) {
      return false;
    }
    double x = pts_value_as_number(a), y = pts_value_as_number(b);
    return x == y || (x != x && y != y);
  }
  if (!pts_value_is_object(a) || !pts_value_is_object(b)) {
    return false;
  }
  const pts_object *x = pts_value_as_object(a), *y = pts_value_as_object(b);
  if (x->type != y->type) {
    return false;
  }
  switch (x->type->kind) {
  case PTS_KIND_STRING:
    return pts_string_equals((const pts_string *)x, (const pts_string *)y);
  case PTS_KIND_INT64:
    return ((const pts_boxed_int64 *)x)->value == ((const pts_boxed_int64 *)y)->value;
  case PTS_KIND_UINT64:
    return ((const pts_boxed_uint64 *)x)->value == ((const pts_boxed_uint64 *)y)->value;
  default:
    return false;
  }
}

/* keys_equal is SameValueZero on two keys of the given kind. */
static bool keys_equal(pts_field_kind kind, uint64_t a, uint64_t b) {
  switch (kind) {
  case PTS_FIELD_NUMBER: {
    double x, y;
    memcpy(&x, &a, sizeof x);
    memcpy(&y, &b, sizeof y);
    return x == y || (x != x && y != y);
  }
  case PTS_FIELD_F32: {
    float x, y;
    memcpy(&x, &a, sizeof x);
    memcpy(&y, &b, sizeof y);
    return x == y || (x != x && y != y);
  }
  case PTS_FIELD_STRING:
    return pts_string_equals((const pts_string *)(uintptr_t)a, (const pts_string *)(uintptr_t)b);
  case PTS_FIELD_VALUE:
    return values_equal(a, b);
  default:
    return a == b;
  }
}

/* word_of reads a key or value of the given kind from slot, zero-extended to a word. */
static uint64_t word_of(pts_field_kind kind, const void *slot) {
  uint64_t word = 0;
  memcpy(&word, slot, pts_field_kind_size(kind));
  return word;
}

static pts_map_table *load_table(const pts_map *map) { return __atomic_load_n(&map->table, __ATOMIC_ACQUIRE); }

static size_t load_used(const pts_map_table *table) {
  size_t used = __atomic_load_n(&table->used, __ATOMIC_ACQUIRE);
  return used < table->capacity ? used : table->capacity;
}

static pts_map_table *new_table(const pts_type *type, size_t capacity) {
  size_t index_slots = 8;
  while (index_slots < 2 * capacity) {
    index_slots *= 2;
  }
  size_t size = sizeof(pts_map_table) + capacity * sizeof(pts_map_entry) + index_slots * sizeof(uint32_t);
  /* Always scanned: besides keys and values, next keeps the newer tables alive for iterators on this one. */
  pts_map_table *table = GC_MALLOC(size);
  if (table == NULL || capacity > UINT32_MAX / 2) {
    pts_panic("RangeError", "Out of memory allocating a %s of %zu entries", type->name, capacity);
  }
  memset(table, 0, size);
  table->capacity = capacity;
  table->index_mask = index_slots - 1;
  table->index = (uint32_t *)(table->entries + capacity);
  return table;
}

/* find returns the number of the live entry whose key is key (of hash h) in table, or -1. */
static int64_t find(const pts_type *type, const pts_map_table *table, uint64_t key, uint64_t h) {
  size_t used = load_used(table);
  for (size_t slot = h & table->index_mask;; slot = (slot + 1) & table->index_mask) {
    uint32_t number = __atomic_load_n(&table->index[slot], __ATOMIC_ACQUIRE);
    if (number == 0) {
      return -1;
    }
    if (number - 1 >= used) {
      continue; /* an entry a racing thread hasn't published yet */
    }
    const pts_map_entry *entry = &table->entries[number - 1];
    if (__atomic_load_n(&entry->hash, __ATOMIC_RELAXED) == h &&
        keys_equal(type->key, __atomic_load_n(&entry->key, __ATOMIC_RELAXED), key)) {
      return number - 1;
    }
  }
}

/* index_entry makes table's index find entry number. */
static void index_entry(pts_map_table *table, size_t number, uint64_t h) {
  size_t slot = h & table->index_mask;
  while (table->index[slot] != 0) {
    slot = (slot + 1) & table->index_mask;
  }
  __atomic_store_n(&table->index[slot], (uint32_t)number + 1, __ATOMIC_RELEASE);
}

/* replace publishes a new table holding the live entries of map's current one, with room
 * for capacity entries and at least one more than it holds. */
static pts_map_table *replace(pts_map *map, size_t capacity) {
  pts_map_table *old = load_table(map);
  size_t used = load_used(old), live = 0;
  for (size_t i = 0; i < used; i++) {
    live += __atomic_load_n(&old->entries[i].hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED;
  }
  if (capacity <= live) {
    capacity = live + 1;
  }
  pts_map_table *table = new_table(map->type, capacity < MIN_CAPACITY ? MIN_CAPACITY : capacity);
  for (size_t i = 0; i < used && table->used < table->capacity; i++) {
    pts_map_entry entry = old->entries[i];
    if (entry.hash != PTS_MAP_DELETED) {
      table->entries[table->used] = entry;
      index_entry(table, table->used, entry.hash);
      table->used++;
    }
  }
  __atomic_store_n(&old->next, table, __ATOMIC_RELEASE);
  __atomic_store_n(&map->table, table, __ATOMIC_RELEASE);
  return table;
}

pts_map *pts_map_new(const pts_type *type) {
  pts_map *map = pts_object_new(type);
  map->table = new_table(type, MIN_CAPACITY);
  return map;
}

pts_map *pts_map_copy(const pts_type *type, const pts_map *source) {
  pts_map *map = pts_map_new(type);
  pts_map_iterator iterator = pts_map_iterate((pts_map *)source);
  while (pts_map_next(&iterator)) {
    pts_map_set(map, &iterator.key, type->kind == PTS_KIND_MAP ? &iterator.value : NULL);
  }
  return map;
}

pts_map *pts_set_of_array(const pts_type *type, const pts_array *array) {
  pts_map *set = pts_map_new(type);
  size_t size = pts_field_kind_size(type->key);
  for (size_t i = 0; i < pts_array_length_of(array); i++) {
    const void *slot = pts_array_slot_or_null(array, (double)i, size);
    if (slot != NULL) {
      pts_map_set(set, slot, NULL);
    }
  }
  return set;
}

bool pts_map_has(const pts_map *map, const void *key) {
  uint64_t word = word_of(map->type->key, key);
  return find(map->type, load_table(map), word, key_hash(map->type->key, word)) >= 0;
}

bool pts_map_get(const pts_map *map, const void *key, void *value) {
  uint64_t word = word_of(map->type->key, key);
  const pts_map_table *table = load_table(map);
  int64_t number = find(map->type, table, word, key_hash(map->type->key, word));
  if (number < 0) {
    return false;
  }
  uint64_t found = __atomic_load_n(&table->entries[number].value, __ATOMIC_RELAXED);
  memcpy(value, &found, pts_field_kind_size(map->type->element));
  return true;
}

pts_value pts_map_get_value(const pts_map *map, const void *key) {
  uint64_t value;
  if (!pts_map_get(map, key, &value)) {
    return PTS_VALUE_UNDEFINED;
  }
  return pts_value_of_slot(map->type->element, &value);
}

void pts_map_key_error(void) { pts_panic("RangeError", "Map has no entry for the key"); }

pts_map *pts_map_set(pts_map *map, const void *key, const void *value) {
  uint64_t word = word_of(map->type->key, key);
  uint64_t h = key_hash(map->type->key, word);
  uint64_t value_word = value == NULL ? 0 : word_of(map->type->element, value);
  pts_map_table *table = load_table(map);
  int64_t number = find(map->type, table, word, h);
  if (number >= 0) {
    if (map->type->kind == PTS_KIND_MAP) {
      __atomic_store_n(&table->entries[number].value, value_word, __ATOMIC_RELAXED);
    }
    return map;
  }
  size_t used = load_used(table);
  if (used == table->capacity) {
    table = replace(map, 2 * (size_t)pts_map_size(map) + 1);
    used = table->used;
  }
  pts_map_entry *entry = &table->entries[used];
  __atomic_store_n(&entry->key, word, __ATOMIC_RELAXED);
  __atomic_store_n(&entry->value, value_word, __ATOMIC_RELAXED);
  __atomic_store_n(&entry->hash, h, __ATOMIC_RELAXED);
  __atomic_store_n(&table->used, used + 1, __ATOMIC_RELEASE);
  index_entry(table, used, h);
  __atomic_store_n(&map->size, map->size + 1, __ATOMIC_RELEASE);
  return map;
}

bool pts_map_delete(pts_map *map, const void *key) {
  uint64_t word = word_of(map->type->key, key);
  pts_map_table *table = load_table(map);
  int64_t number = find(map->type, table, word, key_hash(map->type->key, word));
  if (number < 0) {
    return false;
  }
  __atomic_store_n(&table->entries[number].hash, PTS_MAP_DELETED, __ATOMIC_RELEASE);
  __atomic_store_n(&map->size, map->size - 1, __ATOMIC_RELEASE);
  if (map->size < table->used / 4 && table->used > 4 * MIN_CAPACITY) {
    replace(map, 2 * map->size); /* compact once mostly deleted */
  }
  return true;
}

void pts_map_clear(pts_map *map) {
  pts_map_table *table = load_table(map);
  size_t used = load_used(table);
  for (size_t i = 0; i < used; i++) {
    __atomic_store_n(&table->entries[i].hash, PTS_MAP_DELETED, __ATOMIC_RELEASE);
  }
  __atomic_store_n(&map->size, 0, __ATOMIC_RELEASE);
  replace(map, MIN_CAPACITY);
}

pts_map_iterator pts_map_iterate(pts_map *map) { return (pts_map_iterator){map, load_table(map), 0, 0, 0}; }

/* follow moves an iterator to the table that replaced its own: its position becomes the
 * number of live entries before it, which the new table keeps, in order, at its start. */
static void follow(pts_map_iterator *iterator) {
  pts_map_table *next;
  while ((next = __atomic_load_n(&iterator->table->next, __ATOMIC_ACQUIRE)) != NULL) {
    size_t live = 0;
    for (size_t i = 0; i < iterator->position && i < load_used(iterator->table); i++) {
      if (__atomic_load_n(&iterator->table->entries[i].hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED) {
        live++;
      }
    }
    iterator->table = next;
    iterator->position = live;
  }
}

bool pts_map_next(pts_map_iterator *iterator) {
  follow(iterator);
  const pts_map_table *table = iterator->table;
  for (size_t used = load_used(table); iterator->position < used; iterator->position++) {
    const pts_map_entry *entry = &table->entries[iterator->position];
    if (__atomic_load_n(&entry->hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED) {
      iterator->key = __atomic_load_n(&entry->key, __ATOMIC_RELAXED);
      iterator->value = __atomic_load_n(&entry->value, __ATOMIC_RELAXED);
      iterator->position++;
      return true;
    }
  }
  return false;
}
