/*
 * Map and Set (D-046): an ordered hash table. Entries are appended in insertion order and
 * marked deleted in place; an open-addressing index (twice the entries' capacity, so it
 * has empty slots) finds them by hash. Growing, compacting and clearing build a new table
 * and publish it, so threads racing on a map never read or write out of bounds (D-L13);
 * iterators follow the chain of tables (JS's live iteration). Racing threads may lose
 * entries, add a key twice or fill the index with slots of entries appended over each
 * other, so probes stop after a full round, and the size is a hint that never leaves
 * the range 0 .. the entries ever added (D-130).
 */
#include "pts.h"

#include <stdlib.h>
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
  size_t slot = h & table->index_mask;
  for (size_t probes = 0; probes <= table->index_mask; probes++, slot = (slot + 1) & table->index_mask) {
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
  return -1; /* a full index, which only racing appends make */
}

/* index_entry makes table's index find entry number, unless racing appends have filled it (the entry is lost to lookups then). */
static void index_entry(pts_map_table *table, size_t number, uint64_t h) {
  size_t slot = h & table->index_mask;
  for (size_t probes = 0; probes <= table->index_mask; probes++, slot = (slot + 1) & table->index_mask) {
    if (__atomic_load_n(&table->index[slot], __ATOMIC_RELAXED) == 0) {
      __atomic_store_n(&table->index[slot], (uint32_t)number + 1, __ATOMIC_RELEASE);
      return;
    }
  }
}

/* replace publishes a new table holding the live entries of map's current one, with room for twice as many and one more. */
static pts_map_table *replace(pts_map *map) {
  pts_map_table *old = load_table(map);
  size_t used = load_used(old), live = 0;
  for (size_t i = 0; i < used; i++) {
    live += __atomic_load_n(&old->entries[i].hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED;
  }
  size_t capacity = 2 * live + 1;
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

bool pts_value_is_map(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_MAP;
}

bool pts_value_is_set(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_SET;
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
    pts_map_set(map, &iterator.key, type->kind == PTS_KIND_SET ? NULL : &iterator.value);
  }
  return map;
}

pts_map *pts_set_of_array(const pts_type *type, const pts_array *array, bool undefined_holes) {
  pts_map *set = pts_map_new(type);
  for (size_t i = 0; i < pts_array_length_of(array); i++) {
    uint64_t element = 0;
    if (pts_array_load(array, (double)i, &element)) {
      pts_map_set(set, &element, NULL);
    } else if (undefined_holes) {
      pts_value undefined = PTS_VALUE_UNDEFINED; /* the array's iterator reads a hole as undefined (D-183) */
      pts_map_set(set, &undefined, NULL);
    } /* a set of strings or objects can't hold the undefined JS adds: holes are left out (D-130) */
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
  pts_field_kind kind = map->type->element;
  if (found == 0 && map->type->kind == PTS_KIND_RECORD && (kind == PTS_FIELD_STRING || kind == PTS_FIELD_VALUE || kind == PTS_FIELD_OBJECT)) {
    return false; /* a hole: a match's group that took no part (D-133), undefined as an array's holes are (D-038) */
  }
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

void pts_map_key_error(void) { pts_throw_new(&pts_type_builtin_RangeError, "Map has no entry for the key"); }

/* key_text is a hash-map object's key as a C string, for a message. */
static char *key_text(const pts_string *key) {
  size_t length;
  const char *bytes = pts_string_to_utf8(key, &length);
  char *text = GC_MALLOC_ATOMIC(length + 1);
  if (text == NULL) {
    pts_panic("RangeError", "Out of memory formatting a key of %zu bytes", length);
  }
  memcpy(text, bytes, length);
  text[length] = '\0';
  return text;
}

/* The TypeError of a write to key of a frozen hash-map object (D-157): read only if it has the key, else not extensible. */
void pts_throw_frozen_key(const pts_map *record, const pts_string *key) {
  char *name = key_text(key);
  if (pts_map_has(record, &key)) {
    pts_throw_read_only((const pts_object *)record, name);
  }
  pts_throw_not_extensible(name);
}

pts_map *pts_map_set(pts_map *map, const void *key, const void *value) {
  uint64_t word = word_of(map->type->key, key);
  uint64_t h = key_hash(map->type->key, word);
  uint64_t value_word = value == NULL ? 0 : word_of(map->type->element, value);
  pts_map_table *table = load_table(map);
  int64_t number = find(map->type, table, word, h);
  if (number >= 0) {
    if (map->type->kind != PTS_KIND_SET) {
      __atomic_store_n(&table->entries[number].value, value_word, __ATOMIC_RELAXED);
    }
    return map;
  }
  size_t used = load_used(table);
  if (used == table->capacity) {
    table = replace(map);
    used = load_used(table);
    if (used == table->capacity) {
      return map; /* racing appends filled the new table already: this entry is lost */
    }
  }
  pts_map_entry *entry = &table->entries[used];
  __atomic_store_n(&entry->key, word, __ATOMIC_RELAXED);
  __atomic_store_n(&entry->value, value_word, __ATOMIC_RELAXED);
  __atomic_store_n(&entry->hash, h, __ATOMIC_RELAXED);
  __atomic_store_n(&table->used, used + 1, __ATOMIC_RELEASE);
  index_entry(table, used, h);
  __atomic_store_n(&map->size, map->size + 1, __ATOMIC_RELEASE); /* not atomic: see D-130 */
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
  if (pts_map_size(map) < (double)(table->used / 4) && table->used > 4 * MIN_CAPACITY) {
    replace(map); /* compact once mostly deleted */
  }
  return true;
}

/* Only hash-map objects freeze (D-157): JS leaves a Map's and a Set's entries writable, so their writes test nothing. */
pts_map *pts_record_set(pts_map *record, const void *key, const void *value) {
  if (__builtin_expect(record->type->frozen, 0)) {
    pts_throw_frozen_key(record, *(pts_string *const *)key);
  }
  return pts_map_set(record, key, value);
}

bool pts_record_delete(pts_map *record, const void *key) {
  if (__builtin_expect(record->type->frozen, 0) && pts_map_has(record, key)) {
    pts_throw_undeletable((const pts_object *)record, key_text(*(pts_string *const *)key));
  }
  return pts_map_delete(record, key);
}

void pts_map_clear(pts_map *map) {
  pts_map_table *table = load_table(map);
  size_t used = load_used(table);
  for (size_t i = 0; i < used; i++) {
    __atomic_store_n(&table->entries[i].hash, PTS_MAP_DELETED, __ATOMIC_RELEASE);
  }
  __atomic_store_n(&map->size, 0, __ATOMIC_RELEASE);
  replace(map);
}

pts_map_iterator pts_map_iterate(pts_map *map) { return (pts_map_iterator){map, load_table(map), 0, 0, 0}; }

/* follow moves a position in table to the table that replaced it, and returns that table:
 * the position becomes the number of live entries before it, which the new table keeps,
 * in order, at its start. */
static pts_map_table *follow(pts_map_table *table, size_t *position) {
  pts_map_table *next;
  while ((next = __atomic_load_n(&table->next, __ATOMIC_ACQUIRE)) != NULL) {
    size_t live = 0;
    for (size_t i = 0, used = load_used(table); i < *position && i < used; i++) {
      if (__atomic_load_n(&table->entries[i].hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED) {
        live++;
      }
    }
    table = next;
    *position = live;
  }
  return table;
}

bool pts_map_next(pts_map_iterator *iterator) {
  size_t position = iterator->position;
  pts_map_table *table = follow(iterator->table, &position);
  bool found = false;
  for (size_t used = load_used(table); position < used && !found; position++) {
    const pts_map_entry *entry = &table->entries[position];
    if (__atomic_load_n(&entry->hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED) {
      iterator->key = __atomic_load_n(&entry->key, __ATOMIC_RELAXED);
      iterator->value = __atomic_load_n(&entry->value, __ATOMIC_RELAXED);
      found = true;
    }
  }
  iterator->table = table;
  iterator->position = position;
  return found;
}

/* ---------------------------------------------------------------- hash-map objects */

/* array_index_of reports whether key is an array index, the canonical decimal form of an
 * integer below 2^32 - 1 ("0", "17"; not "017" or "4294967295"), and stores it in *index. */
static bool array_index_of(const pts_string *key, uint32_t *index) {
  enum { MAX_DIGITS = 10 };
  if (key->byte_len == 0 || key->byte_len > MAX_DIGITS || (key->bytes[0] == '0' && key->byte_len > 1)) {
    return false;
  }
  uint64_t value = 0;
  for (size_t i = 0; i < key->byte_len; i++) {
    if (key->bytes[i] < '0' || key->bytes[i] > '9') {
      return false;
    }
    value = value * 10 + (uint64_t)(key->bytes[i] - '0');
  }
  if (value >= UINT32_MAX) {
    return false;
  }
  *index = (uint32_t)value;
  return true;
}

/* An entry of a hash-map object in its place in JS's key order: array indices by value,
 * then the other keys (index NOT_AN_INDEX) by insertion position. */
typedef struct ordered_entry {
  uint32_t index;
  size_t position;
  pts_map_entry entry;
} ordered_entry;

enum { NOT_AN_INDEX = UINT32_MAX };

static int compare_ordered(const void *a, const void *b) {
  const ordered_entry *x = a, *y = b;
  if (x->index != y->index) {
    return x->index < y->index ? -1 : 1;
  }
  return x->position < y->position ? -1 : x->position > y->position;
}

static bool has_array_index_key(const pts_map_table *table) {
  uint32_t index;
  for (size_t i = 0, used = load_used(table); i < used; i++) {
    const pts_map_entry *entry = &table->entries[i];
    if (__atomic_load_n(&entry->hash, __ATOMIC_ACQUIRE) != PTS_MAP_DELETED &&
        array_index_of((const pts_string *)(uintptr_t)__atomic_load_n(&entry->key, __ATOMIC_RELAXED), &index)) {
      return true;
    }
  }
  return false;
}

/* ordered_snapshot is a table, never published, of table's live entries in JS's key order. */
static pts_map_table *ordered_snapshot(const pts_map_table *table) {
  size_t used = load_used(table), count = 0;
  ordered_entry *ordered = GC_MALLOC((used ? used : 1) * sizeof *ordered); /* scanned: a racing delete may leave it the only reference to a key */
  if (ordered == NULL) {
    pts_panic("RangeError", "Out of memory listing the keys of an object of %zu entries", used);
  }
  for (size_t i = 0; i < used; i++) {
    const pts_map_entry *entry = &table->entries[i];
    pts_map_entry copy = {
        .hash = __atomic_load_n(&entry->hash, __ATOMIC_ACQUIRE),
        .key = __atomic_load_n(&entry->key, __ATOMIC_RELAXED),
        .value = __atomic_load_n(&entry->value, __ATOMIC_RELAXED),
    };
    if (copy.hash == PTS_MAP_DELETED) {
      continue;
    }
    uint32_t index;
    if (!array_index_of((const pts_string *)(uintptr_t)copy.key, &index)) {
      index = NOT_AN_INDEX;
    }
    ordered[count++] = (ordered_entry){index, i, copy};
  }
  qsort(ordered, count, sizeof *ordered, compare_ordered);
  pts_map_table *snapshot = GC_MALLOC(sizeof *snapshot + count * sizeof(pts_map_entry));
  if (snapshot == NULL) {
    pts_panic("RangeError", "Out of memory listing the keys of an object of %zu entries", count);
  }
  for (size_t i = 0; i < count; i++) {
    snapshot->entries[i] = ordered[i].entry;
  }
  GC_FREE(ordered);
  snapshot->capacity = snapshot->used = count; /* no index and no next: only iterated */
  return snapshot;
}

pts_map_iterator pts_record_iterate(pts_map *record) {
  pts_map_table *table = load_table(record);
  if (!has_array_index_key(table)) {
    return (pts_map_iterator){record, table, 0, 0, 0};
  }
  return (pts_map_iterator){record, ordered_snapshot(table), 0, 0, 0};
}

void pts_record_key_error(const pts_string *key) {
  pts_throw_new(&pts_type_builtin_RangeError, "Object has no property '%.*s'" PTS_MISSING_KEY_HINT, (int)key->byte_len, key->bytes);
}

pts_map *pts_map_assign(pts_map *map, pts_map *source) {
  pts_map_iterator iterator = source->type->kind == PTS_KIND_RECORD ? pts_record_iterate(source) : pts_map_iterate(source);
  while (pts_map_next(&iterator)) {
    pts_map_set(map, &iterator.key, source->type->kind == PTS_KIND_SET ? NULL : &iterator.value);
  }
  return map;
}
