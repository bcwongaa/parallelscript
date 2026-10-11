/* console.log of instances (inspect.c), here mainly to run it under the sanitizers. */
#include "pts.h"

#include "check.h"

#include <stdio.h>
#include <stdlib.h>

void pts_inspect_write(FILE *out, const pts_object *object);

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
  char *text = NULL;
  size_t len = 0;
  FILE *out = open_memstream(&text, &len);
  pts_inspect_write(out, object);
  fclose(out);
  return text;
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

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  circular_references_are_numbered();
  deep_instances_print_their_name();
  plain_objects_print_without_a_name();
  return CHECK_DONE();
}
