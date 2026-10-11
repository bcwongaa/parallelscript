/*
 * console.table: a port of Node's console.table (lib/internal/console/constructor.js),
 * which makes a column per property of the rows, and of the table it draws
 * (lib/internal/cli_table.js), D-088.
 */
#include "inspect_internal.h"

#include <math.h>

/* A column: its heading and a cell per row; a cell with NULL bytes is one Node's sparse array lacks, drawn empty. */
typedef struct column {
  text heading;
  text *cells;
  size_t length; /* the rows up to its last cell, as the length of Node's array */
} column;

typedef struct table {
  column *columns;
  size_t count, rows;
} table;

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory drawing a console.table");
  }
  return memory;
}

static column *add_column(table *t, const char *heading, size_t len) {
  t->columns = checked(realloc(t->columns, (t->count + 1) * sizeof *t->columns));
  column *c = &t->columns[t->count++];
  *c = (column){.cells = checked(calloc(t->rows ? t->rows : 1, sizeof(text)))};
  text_add(&c->heading, heading, len);
  return c;
}

static void set_cell(column *c, size_t row, text cell) {
  free(c->cells[row].bytes);
  text_add(&cell, "", 0); /* an empty cell that exists has bytes */
  c->cells[row] = cell;
  if (row + 1 > c->length) {
    c->length = row + 1;
  }
}

static text cell_of(pts_value value) {
  text cell = {0};
  pts_inspect_cell(&cell, value);
  return cell;
}

static text raw_cell(const pts_string *string) {
  text cell = {0};
  text_add(&cell, string->bytes, string->byte_len);
  return cell;
}

/* Whether a value is a primitive for console.table: not an object or a function. */
static bool is_primitive(pts_value value) {
  if (!pts_value_is_object(value)) {
    return true;
  }
  pts_kind kind = pts_value_as_object(value)->type->kind;
  return kind == PTS_KIND_STRING || kind == PTS_KIND_INT64 || kind == PTS_KIND_UINT64;
}

/* The canonical array index a key spells (as JS orders an object's keys), or -1. */
static int64_t array_index(const text *key) {
  if (key->len == 0 || key->len > 10 || (key->len > 1 && key->bytes[0] == '0')) {
    return -1;
  }
  int64_t index = 0;
  for (size_t i = 0; i < key->len; i++) {
    if (key->bytes[i] < '0' || key->bytes[i] > '9') {
      return -1;
    }
    index = index * 10 + (key->bytes[i] - '0');
  }
  return index < 4294967295 ? index : -1;
}

/* The column of a property, made after the others if it is new. */
static column *property_column(table *t, size_t first, const pts_string *key) {
  for (size_t i = first; i < t->count; i++) {
    if (t->columns[i].heading.len == key->byte_len && memcmp(t->columns[i].heading.bytes, key->bytes, key->byte_len) == 0) {
      return &t->columns[i];
    }
  }
  return add_column(t, key->bytes, key->byte_len);
}

/* Orders the property columns from first on as a null-prototype object's keys: array indices ascending, then the rest as made. */
static void order_property_columns(table *t, size_t first) {
  for (size_t i = first + 1; i < t->count; i++) { /* an insertion sort, stable for the keys that aren't indices */
    column moving = t->columns[i];
    int64_t index = array_index(&moving.heading);
    size_t j = i;
    while (index >= 0 && j > first && (array_index(&t->columns[j - 1].heading) < 0 || array_index(&t->columns[j - 1].heading) > index)) {
      t->columns[j] = t->columns[j - 1];
      j--;
    }
    t->columns[j] = moving;
  }
}

/*
 * A table of an array or object: a row per own key, a column per property of the rows
 * that are objects (only the properties given, if any), and a "Values" column for the
 * rows that are primitives.
 */
static void property_rows(table *t, pts_value data, pts_value properties) {
  pts_array *row_keys = pts_value_key_array(data);
  t->rows = pts_array_length_of(row_keys);
  column *index = add_column(t, "(index)", 7);
  for (size_t i = 0; i < t->rows; i++) {
    set_cell(index, i, raw_cell(pts_value_to_string(pts_array_element(row_keys, (double)i))));
  }
  text *values = checked(calloc(t->rows ? t->rows : 1, sizeof *values));
  size_t values_length = 0;
  bool given = properties != PTS_VALUE_UNDEFINED;
  for (size_t i = 0; i < t->rows; i++) {
    pts_value item = pts_dynamic_get(data, pts_array_element(row_keys, (double)i));
    bool primitive = is_primitive(item);
    if (!given && primitive) {
      values[i] = cell_of(item);
      text_add(&values[i], "", 0);
      values_length = i + 1;
      continue;
    }
    pts_array *keys = given ? (pts_array *)pts_value_as_object(properties) : pts_value_key_array(item);
    for (size_t k = 0; k < pts_array_length_of(keys); k++) {
      pts_value key = pts_array_element(keys, (double)k);
      column *c = property_column(t, 1, pts_value_to_string(key));
      bool own = !(primitive && given) && (!given || pts_value_has_own(item, key));
      set_cell(c, i, own ? cell_of(pts_dynamic_get(item, key)) : (text){0});
    }
  }
  order_property_columns(t, 1);
  if (values_length > 0) {
    column *c = add_column(t, "Values", 6);
    free(c->cells);
    c->cells = values;
    c->length = values_length;
  } else {
    free(values);
  }
}

/* A table of a map (an iteration index, the key and the value of each entry) or a set (an index and each value). */
static void entry_rows(table *t, pts_map *map) {
  bool is_map = map->type->kind == PTS_KIND_MAP;
  t->rows = (size_t)pts_map_size(map);
  add_column(t, "(iteration index)", 17);
  if (is_map) {
    add_column(t, "Key", 3);
  }
  add_column(t, "Values", 6);
  column *index = &t->columns[0], *keys = is_map ? &t->columns[1] : NULL, *values = &t->columns[t->count - 1];
  pts_map_iterator iterator = pts_map_iterate(map);
  for (size_t i = 0; i < t->rows && pts_map_next(&iterator); i++) {
    set_cell(index, i, cell_of(pts_value_from_number((double)i)));
    pts_value key = pts_value_of_slot(map->type->key, &iterator.key);
    if (is_map) {
      set_cell(keys, i, cell_of(key));
      set_cell(values, i, cell_of(pts_value_of_slot(map->type->element, &iterator.value)));
    } else {
      set_cell(values, i, cell_of(key));
    }
  }
}

static void repeat(text *out, const char *piece, size_t count) {
  for (size_t i = 0; i < count; i++) {
    text_puts(out, piece);
  }
}

/* cli_table's divider line: left, each column's width of horizontal bars joined by middle, right. */
static void divider(text *out, const size_t *widths, size_t count, const char *left, const char *middle, const char *right) {
  text_puts(out, left);
  for (size_t i = 0; i < count; i++) {
    repeat(out, "─", widths[i] + 2);
    text_puts(out, i + 1 < count ? middle : right);
  }
}

/* cli_table's renderRow: each cell left-justified in its column, between bars. */
static void render_row(text *out, const text *const *cells, const size_t *widths, size_t count) {
  text_puts(out, "│ ");
  for (size_t i = 0; i < count; i++) {
    const text *cell = cells[i];
    size_t width = pts_text_width(cell->bytes, cell->len);
    text_add(out, cell->bytes, cell->len);
    repeat(out, " ", widths[i] > width ? widths[i] - width : 0);
    if (i + 1 < count) {
      text_puts(out, " │ ");
    }
  }
  text_puts(out, " │");
}

/* cli_table: the headings and the rows in boxes, each column as wide as its widest cell. */
static void draw(text *out, const table *t) {
  static const text empty = {.bytes = "", .len = 0};
  size_t *widths = checked(calloc(t->count, sizeof *widths));
  const text **row = checked(calloc(t->count, sizeof *row));
  size_t longest = 0;
  for (size_t i = 0; i < t->count; i++) {
    longest = t->columns[i].length > longest ? t->columns[i].length : longest;
  }
  for (size_t i = 0; i < t->count; i++) {
    const column *c = &t->columns[i];
    widths[i] = pts_text_width(c->heading.bytes, c->heading.len);
    for (size_t j = 0; j < longest && j < c->length; j++) {
      size_t width = c->cells[j].bytes == NULL ? 0 : pts_text_width(c->cells[j].bytes, c->cells[j].len);
      widths[i] = width > widths[i] ? width : widths[i];
    }
    row[i] = &c->heading;
  }
  divider(out, widths, t->count, "┌", "┬", "┐\n");
  render_row(out, row, widths, t->count);
  divider(out, widths, t->count, "\n├", "┼", "┤\n");
  for (size_t j = 0; j < longest; j++) {
    for (size_t i = 0; i < t->count; i++) {
      const column *c = &t->columns[i];
      row[i] = j < c->length && c->cells[j].bytes != NULL ? &c->cells[j] : &empty;
    }
    render_row(out, row, widths, t->count);
    text_puts(out, "\n");
  }
  divider(out, widths, t->count, "└", "┴", "┘");
  free(widths);
  free(row);
}

char *pts_table(pts_value data, pts_value properties, size_t *len) {
  table t = {0};
  pts_kind kind = pts_value_as_object(data)->type->kind;
  if (kind == PTS_KIND_MAP || kind == PTS_KIND_SET) {
    entry_rows(&t, (pts_map *)pts_value_as_object(data));
  } else {
    property_rows(&t, data, properties);
  }
  text out = {0};
  draw(&out, &t);
  for (size_t i = 0; i < t.count; i++) {
    for (size_t j = 0; j < t.rows; j++) {
      free(t.columns[i].cells[j].bytes);
    }
    free(t.columns[i].cells);
    free(t.columns[i].heading.bytes);
  }
  free(t.columns);
  *len = out.len;
  return out.bytes;
}
