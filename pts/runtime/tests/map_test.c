/* Maps, sets and hash-map objects: SameValueZero keys, insertion order, deletion, growth,
 * iteration that follows replaced tables, and JS's key order for objects. */
#include "pts.h"

#include "check.h"

static const pts_type number_map_type = {
    .kind = PTS_KIND_MAP,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Map",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_NUMBER,
    .element = PTS_FIELD_NUMBER,
};

static const pts_type string_set_type = {
    .kind = PTS_KIND_SET,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Set",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_STRING,
};

static const pts_type number_record_type = {
    .kind = PTS_KIND_RECORD,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Object",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_STRING,
    .element = PTS_FIELD_NUMBER,
};

static double get(pts_map *map, double key) {
  double value = -1;
  pts_map_get(map, &key, &value);
  return value;
}

static void put(pts_map *map, double key, double value) { pts_map_set(map, &key, &value); }

static void keys_compare_with_same_value_zero(void) {
  pts_map *map = pts_map_new(&number_map_type);
  put(map, 0.0, 1);
  put(map, __builtin_nan(""), 2);
  CHECK(get(map, -0.0) == 1);
  CHECK(get(map, -__builtin_nan("")) == 2);
  CHECK(pts_map_size(map) == 2);
  pts_map *set = pts_map_new(&string_set_type);
  pts_string *a1 = pts_string_from_cstr("abc"), *a2 = pts_string_concat(pts_string_from_cstr("ab"), pts_string_from_cstr("c"));
  pts_map_set(set, &a1, NULL);
  pts_map_set(set, &a2, NULL);
  CHECK(pts_map_size(set) == 1 && pts_map_has(set, &a2));
}

static void entries_keep_insertion_order_across_growth_and_deletion(void) {
  pts_map *map = pts_map_new(&number_map_type);
  for (int i = 0; i < 1000; i++) {
    put(map, i, i * 10);
  }
  for (int i = 0; i < 1000; i += 2) {
    double key = i;
    CHECK(pts_map_delete(map, &key));
  }
  put(map, 0, -1); /* re-added: goes last */
  CHECK(pts_map_size(map) == 501);
  pts_map_iterator iterator = pts_map_iterate(map);
  double expected = 1;
  size_t seen = 0;
  while (pts_map_next(&iterator)) {
    double key = PTS_MAP_ITERATOR_KEY(double, iterator);
    if (seen < 500) {
      CHECK(key == expected && PTS_MAP_ITERATOR_VALUE(double, iterator) == expected * 10);
      expected += 2;
    } else {
      CHECK(key == 0 && PTS_MAP_ITERATOR_VALUE(double, iterator) == -1);
    }
    seen++;
  }
  CHECK(seen == 501);
}

static void iterators_see_additions_and_follow_replaced_tables(void) {
  pts_map *map = pts_map_new(&number_map_type);
  put(map, 1, 1);
  put(map, 2, 2);
  pts_map_iterator iterator = pts_map_iterate(map);
  CHECK(pts_map_next(&iterator) && PTS_MAP_ITERATOR_KEY(double, iterator) == 1);
  for (int i = 3; i <= 100; i++) {
    put(map, i, i); /* grows the table several times */
  }
  double key = 2;
  pts_map_delete(map, &key);
  double expected = 3;
  while (pts_map_next(&iterator)) {
    CHECK(PTS_MAP_ITERATOR_KEY(double, iterator) == expected);
    expected++;
  }
  CHECK(expected == 101);
  pts_map_iterator after_clear = pts_map_iterate(map);
  CHECK(pts_map_next(&after_clear));
  pts_map_clear(map);
  put(map, 7, 7);
  CHECK(pts_map_next(&after_clear) && PTS_MAP_ITERATOR_KEY(double, after_clear) == 7);
  CHECK(!pts_map_next(&after_clear));
}

/* Racing deletes of one entry each take the size down (D-130); below zero it reads as 0, and tables still grow by their live entries. */
static void a_size_below_zero_reads_as_zero(void) {
  pts_map *map = pts_map_new(&number_map_type);
  map->size = -5;
  CHECK(pts_map_size(map) == 0);
  for (int i = 0; i < 100; i++) {
    put(map, i, i);
  }
  CHECK(pts_map_size(map) == 95 && get(map, 99) == 99 && map->table->capacity < 1000);
}

/* Racing appends to one entry can each index it, filling the index (D-130): lookups and appends then end after one round. */
static void probes_end_on_an_index_racing_appends_filled(void) {
  pts_map *map = pts_map_new(&number_map_type);
  put(map, 0, 10);
  pts_map_table *table = map->table;
  for (size_t slot = 0; slot <= table->index_mask; slot++) {
    table->index[slot] = 1;
  }
  CHECK(get(map, 5) == -1 && !pts_map_has(map, &(double){5}));
  put(map, 5, 50); /* appended, but no lookup can find it */
  CHECK(get(map, 0) == 10 && get(map, 5) == -1 && pts_map_size(map) == 2);
  pts_map_iterator iterator = pts_map_iterate(map);
  CHECK(pts_map_next(&iterator) && pts_map_next(&iterator) && PTS_MAP_ITERATOR_KEY(double, iterator) == 5);
}

static void put_string(pts_map *record, const char *key, double value) {
  pts_string *string = pts_string_from_cstr(key);
  pts_map_set(record, &string, &value);
}

/* record_keys joins a record's keys in the order pts_record_iterate gives them. */
static pts_string *record_keys(pts_map *record) {
  pts_string *keys = pts_string_from_cstr("");
  pts_map_iterator iterator = pts_record_iterate(record);
  while (pts_map_next(&iterator)) {
    keys = pts_string_concat(pts_string_concat(keys, PTS_MAP_ITERATOR_KEY(pts_string *, iterator)), pts_string_from_cstr(" "));
  }
  return keys;
}

static void record_keys_list_array_indices_first_in_numeric_order(void) {
  pts_map *record = pts_map_new(&number_record_type);
  const char *inserted[] = {"b", "10", "a", "2", "017", "4294967295", "4294967294", "0", "-1", "1.5"};
  for (size_t i = 0; i < sizeof inserted / sizeof *inserted; i++) {
    put_string(record, inserted[i], (double)i);
  }
  pts_string *deleted = pts_string_from_cstr("a");
  pts_map_delete(record, &deleted);
  CHECK(pts_string_equals(record_keys(record), pts_string_from_cstr("0 2 10 4294967294 b 017 4294967295 -1 1.5 ")));
}

static void record_keys_without_indices_keep_insertion_order(void) {
  pts_map *record = pts_map_new(&number_record_type);
  put_string(record, "z", 1);
  put_string(record, "y", 2);
  put_string(record, "z", 3); /* replaced in place */
  CHECK(pts_string_equals(record_keys(record), pts_string_from_cstr("z y ")));
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  keys_compare_with_same_value_zero();
  entries_keep_insertion_order_across_growth_and_deletion();
  iterators_see_additions_and_follow_replaced_tables();
  a_size_below_zero_reads_as_zero();
  probes_end_on_an_index_racing_appends_filled();
  record_keys_list_array_indices_first_in_numeric_order();
  record_keys_without_indices_keep_insertion_order();
  return CHECK_DONE();
}
