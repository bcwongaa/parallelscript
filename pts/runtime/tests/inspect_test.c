/* console.log of instances (inspect.c), here mainly to run it under the sanitizers. */
#include "pts.h"

#include "check.h"

#include <stdio.h>
#include <stdlib.h>

char *pts_inspect(pts_value value, double depth, bool show_hidden, size_t *len); /* inspect.c */

typedef struct chain {
  const pts_type *type;
  struct chain *next;
  pts_string *label;
  int64_t big;
} chain;

static const pts_field chain_fields[] = {
    {"next", offsetof(chain, next), PTS_FIELD_OBJECT},
    {"label", offsetof(chain, label), PTS_FIELD_STRING},
    {"big", offsetof(chain, big), PTS_FIELD_I64},
};

static const pts_type chain_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(chain, next) | PTS_GC_WORD(chain, label)),
    .name = "Link",
    .size = sizeof(chain),
    .field_count = 3,
    .fields = chain_fields,
};

/* inspected returns what console.log prints for object, without the newline. */
static char *inspected(const void *object) {
  size_t len;
  return pts_inspect(pts_value_from_object((pts_object *)object), 2, false, &len);
}

static chain *new_chain(const char *label, int64_t big) {
  chain *l = pts_object_new(&chain_type);
  l->label = pts_string_from_cstr(label);
  l->big = big;
  l->next = l;
  return l;
}

static void circular_references_are_numbered(void) {
  chain *a = new_chain("a", 1);
  chain *b = new_chain("it's", INT64_MIN);
  a->next = b;
  b->next = a;
  char *text = inspected(a);
  /* The inner instance takes 81 columns on one line, so it breaks, as in Node. */
  CHECK_STR(text, "<ref *1> Link {\n  next: Link {\n    next: [Circular *1],\n    label: \"it's\",\n    big: -9223372036854775808\n  },\n"
                  "  label: 'a',\n  big: 1\n}");
  free(text);
}

static void deep_instances_print_their_name(void) {
  chain *a = new_chain("\t", 0), *b = new_chain("", 0), *c = new_chain("", 0), *d = new_chain("", 0);
  a->next = b;
  b->next = c;
  c->next = d;
  char *text = inspected(a);
  CHECK_STR(text, "Link {\n  next: Link {\n    next: Link { next: [Link], label: '', big: 0 },\n    label: '',\n    big: 0\n  },\n"
                  "  label: '\\t',\n  big: 0\n}");
  free(text);
}

/* An object literal's descriptor is named "Object", which Node prints without a name. */
static const pts_type plain_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(chain, next) | PTS_GC_WORD(chain, label)),
    .name = "Object",
    .size = sizeof(chain),
    .field_count = 3,
    .fields = chain_fields,
};

static const pts_type empty_plain_type = {.kind = PTS_KIND_OBJECT, .gc_descr = PTS_GC_NO_POINTERS, .name = "Object", .size = sizeof(pts_object)};

static void plain_objects_print_without_a_name(void) {
  chain *a = pts_object_new(&plain_type), *b = pts_object_new(&plain_type), *c = pts_object_new(&plain_type);
  a->label = b->label = c->label = pts_string_from_cstr("x");
  a->next = b;
  b->next = c;
  c->next = pts_object_new(&empty_plain_type);
  char *text = inspected(a);
  CHECK_STR(text, "{\n  next: { next: { next: {}, label: 'x', big: 0 }, label: 'x', big: 0 },\n  label: 'x',\n  big: 0\n}");
  free(text);
  b->next = b;
  c->next = a;
  a->next = c;
  text = inspected(a);
  CHECK_STR(text, "<ref *1> {\n  next: { next: [Circular *1], label: 'x', big: 0 },\n  label: 'x',\n  big: 0\n}"); /* as Node prints it */
  free(text);
}

typedef struct options {
  const pts_type *type;
  pts_value verbose;
  pts_value level;
} options;

static const pts_field options_fields[] = {
    {"verbose", offsetof(options, verbose), PTS_FIELD_VALUE},
    {"level", offsetof(options, level), PTS_FIELD_VALUE},
};

static const pts_type options_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(options, verbose) | PTS_GC_WORD(options, level)),
    .name = "Object",
    .size = sizeof(options),
    .field_count = 2,
    .fields = options_fields,
};

/* An optional property an object leaves out (D-065) isn't printed; undefined is. */
static void absent_properties_are_not_printed(void) {
  options *o = pts_object_new(&options_type);
  char *text = inspected(o);
  CHECK_STR(text, "{}");
  free(text);
  o->level = pts_value_from_number(3);
  text = inspected(o);
  CHECK_STR(text, "{ level: 3 }");
  free(text);
  o->verbose = PTS_VALUE_UNDEFINED;
  text = inspected(o);
  CHECK_STR(text, "{ verbose: undefined, level: 3 }");
  free(text);
  CHECK(pts_value_present(PTS_VALUE_ABSENT) == PTS_VALUE_UNDEFINED);
  CHECK(pts_value_present(o->level) == o->level);
}

char *pts_format(size_t argc, const pts_value *argv, size_t *len); /* inspect.c */

static pts_value text_value(const char *cstr) { return pts_value_from_string(pts_string_from_cstr(cstr)); }

/* formatted returns what console.log prints for its argc arguments, without the newline. */
static char *formatted(size_t argc, const pts_value *argv) {
  size_t len;
  return pts_format(argc, argv, &len);
}

/* A format's placeholders take the arguments in order; those left over follow, strings raw. */
static void placeholders_take_the_next_arguments(void) {
  char *text = formatted(4, (pts_value[]){text_value("%s has %d items"), text_value("cart"), pts_value_from_number(3), text_value("extra")});
  CHECK_STR(text, "cart has 3 items extra");
  free(text);
  text = formatted(3, (pts_value[]){text_value("%i|%f"), text_value("42.9px"), text_value("1e3x")});
  CHECK_STR(text, "42|1000");
  free(text);
  text = formatted(2, (pts_value[]){text_value("%c%s"), text_value("color: red")});
  CHECK_STR(text, "%s");
  free(text);
}

/* A placeholder without an argument, an unknown one and a lone % stay; %% is % only where arguments follow. */
static void placeholders_without_arguments_stay(void) {
  char *text = formatted(2, (pts_value[]){text_value("%s and %s"), text_value("one")});
  CHECK_STR(text, "one and %s");
  free(text);
  text = formatted(2, (pts_value[]){text_value("%x %s %"), pts_value_from_number(1)});
  CHECK_STR(text, "%x 1 %");
  free(text);
  text = formatted(1, (pts_value[]){text_value("100%%")});
  CHECK_STR(text, "100%%");
  free(text);
  text = formatted(2, (pts_value[]){text_value("100%%"), pts_value_from_number(5)});
  CHECK_STR(text, "100% 5");
  free(text);
}

/* Without a format, strings print raw and other values inspected, nested strings quoted. */
static void arguments_without_a_format_are_inspected(void) {
  char *text = formatted(3, (pts_value[]){pts_value_from_number(-0.0), text_value("%s"), PTS_VALUE_NULL});
  CHECK_STR(text, "-0 %s null");
  free(text);
}

/* A long string nested in an object is split after its line breaks; one beyond 10,000 units is cut. */
static void long_strings_are_split_and_cut(void) {
  size_t len;
  char *text = pts_inspect(text_value("first line of a long text\nsecond line of a long text\nthird line of a long text"), 2, false, &len);
  CHECK_STR(text, "'first line of a long text\\n' +\n  'second line of a long text\\n' +\n  'third line of a long text'");
  free(text);
  char *huge = malloc(10003);
  memset(huge, 'x', 10002);
  huge[10002] = '\0';
  text = pts_inspect(text_value(huge), 2, false, &len);
  CHECK(len == 10000 + 2 + strlen("... 2 more characters"));
  CHECK(strcmp(text + len - strlen("'... 2 more characters"), "'... 2 more characters") == 0);
  free(text);
  free(huge);
}

char *pts_table(pts_value data, pts_value properties, size_t *len); /* table.c */

/* console.table of an array of primitives: a row per index and a "Values" column, in boxes. */
static void tables_draw_a_row_per_key(void) {
  pts_string *elements[] = {pts_string_from_cstr("a"), pts_string_from_cstr("日本")};
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_STRING], elements, 2);
  size_t len;
  char *text = pts_table(pts_value_from_object((pts_object *)array), PTS_VALUE_UNDEFINED, &len);
  CHECK_STR(text, "┌─────────┬────────┐\n"
                  "│ (index) │ Values │\n"
                  "├─────────┼────────┤\n"
                  "│ 0       │ 'a'    │\n"
                  "│ 1       │ '日本' │\n"
                  "└─────────┴────────┘");
  free(text);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  circular_references_are_numbered();
  deep_instances_print_their_name();
  plain_objects_print_without_a_name();
  absent_properties_are_not_printed();
  placeholders_take_the_next_arguments();
  placeholders_without_arguments_stay();
  arguments_without_a_format_are_inspected();
  long_strings_are_split_and_cut();
  tables_draw_a_row_per_key();
  return CHECK_DONE();
}
