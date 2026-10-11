/* Objects: type descriptors, allocation by layout, and collection, also from several threads. */
#include "pts.h"

#include "check.h"

#include <gc_inline.h> /* GC_I_PTRFREE */
#include <pthread.h>
#include <stdio.h>

enum { THREADS_RACING = 4 };

/* A list node: a number the collector must not scan, and two pointers it must. */
typedef struct node {
  const pts_type *type;
  double value;
  pts_string *label;
  struct node *next;
} node;

static const pts_field node_fields[] = {
    {"value", offsetof(node, value), PTS_FIELD_NUMBER},
    {"label", offsetof(node, label), PTS_FIELD_STRING},
    {"next", offsetof(node, next), PTS_FIELD_OBJECT},
};

static const pts_type node_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(1u << 2 | 1u << 3),
    .name = "Node",
    .size = sizeof(node),
    .field_count = 3,
    .fields = node_fields,
};

/* A point: numbers only, so instances are pointer-free. */
typedef struct point {
  const pts_type *type;
  double x, y;
} point;

static double point_norm1(pts_object *object) {
  point *p = (point *)object;
  return p->x + p->y;
}

static const pts_field point_fields[] = {{"x", offsetof(point, x), PTS_FIELD_NUMBER}, {"y", offsetof(point, y), PTS_FIELD_NUMBER}};

static const pts_method point_methods[] = {{"norm1", PTS_METHOD, (pts_function)point_norm1}};

static const pts_type point_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Point",
    .size = sizeof(point),
    .field_count = 2,
    .fields = point_fields,
    .method_count = 1,
    .methods = point_methods,
};

/* A point with a label, whose layout extends point's. */
typedef struct labelled_point {
  point base;
  pts_string *label;
} labelled_point;

static const pts_type labelled_point_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(1u << 3),
    .name = "LabelledPoint",
    .parent = &point_type,
    .size = sizeof(labelled_point),
};

static void bitmap_descriptors_name_words_from_the_header(void) {
  CHECK(PTS_GC_POINTER_WORDS(1u << 2) == (((GC_word)1 << 61) | GC_DS_BITMAP));
  CHECK(PTS_GC_POINTER_WORDS(1u) == (((GC_word)1 << 63) | GC_DS_BITMAP));
  CHECK(PTS_GC_ALL_WORDS(48) == 48);
}

static void new_objects_are_zeroed_and_typed(void) {
  node *n = pts_object_new(&node_type);
  CHECK(n->type == &node_type);
  CHECK(n->value == 0 && n->label == NULL && n->next == NULL);
  point *p = pts_object_new(&point_type);
  CHECK(p->type == &point_type);
  CHECK(p->x == 0 && p->y == 0);
}

static void pointer_free_objects_are_allocated_atomic(void) {
  CHECK(GC_get_kind_and_size(pts_object_new(&point_type), NULL) == GC_I_PTRFREE);
  CHECK(GC_get_kind_and_size(pts_object_new(&node_type), NULL) != GC_I_PTRFREE);
}

static void methods_are_found_through_the_descriptor(void) {
  point *p = pts_object_new(&point_type);
  p->x = 1.5;
  p->y = 2;
  double (*norm1)(pts_object *) = (double (*)(pts_object *))point_type.methods[0].function;
  CHECK(norm1((pts_object *)p) == 3.5);
  labelled_point *lp = pts_object_new(&labelled_point_type);
  CHECK(lp->base.type->parent == &point_type);
}

static void instance_of_follows_the_parent_chain(void) {
  labelled_point *lp = pts_object_new(&labelled_point_type);
  point *p = pts_object_new(&point_type);
  CHECK(pts_instance_of(lp, &labelled_point_type));
  CHECK(pts_instance_of(lp, &point_type));
  CHECK(pts_instance_of(p, &point_type));
  CHECK(!pts_instance_of(p, &labelled_point_type));
  CHECK(!pts_instance_of(lp, &node_type));
}

/* An object with a property x at another offset than point's. */
typedef struct tagged {
  const pts_type *type;
  pts_string *tag;
  double x;
} tagged;

static const pts_field tagged_fields[] = {{"tag", offsetof(tagged, tag), PTS_FIELD_STRING}, {"x", offsetof(tagged, x), PTS_FIELD_NUMBER}};

static const pts_type tagged_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(1u << 1),
    .name = "Tagged",
    .size = sizeof(tagged),
    .field_count = 2,
    .fields = tagged_fields,
};

static void field_addresses_are_cached_per_layout(void) {
  static pts_site site;
  point *p = pts_object_new(&point_type);
  tagged *t = pts_object_new(&tagged_type);
  p->x = 1.5;
  t->x = 2.5;
  CHECK(pts_field_address(p, &site, "x") == &p->x);
  CHECK(site.cache == ((uint64_t)(uintptr_t)&point_type | (uint64_t)offsetof(point, x) << 48));
  CHECK(pts_field_address(t, &site, "x") == &t->x);
  CHECK(site.cache == ((uint64_t)(uintptr_t)&tagged_type | (uint64_t)offsetof(tagged, x) << 48));
  CHECK(*(double *)pts_field_address(p, &site, "x") == 1.5);
}

/* An object whose property x a getter and setter provide, over a field named hidden. */
typedef struct computed {
  const pts_type *type;
  double hidden;
  int reads, writes;
} computed;

static double computed_get_x(pts_object *object) {
  computed *c = (computed *)object;
  c->reads++;
  return c->hidden * 10;
}

static void computed_set_x(pts_object *object, double value) {
  computed *c = (computed *)object;
  c->writes++;
  c->hidden = value / 10;
}

static double computed_norm1(pts_object *object) { return ((computed *)object)->hidden; }

static const pts_method computed_methods[] = {
    {"x", PTS_GETTER, (pts_function)computed_get_x},
    {"x", PTS_SETTER, (pts_function)computed_set_x},
    {"norm1", PTS_METHOD, (pts_function)computed_norm1},
};

static const pts_type computed_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Computed",
    .size = sizeof(computed),
    .method_count = 3,
    .methods = computed_methods,
};

static void method_sites_cache_the_index_per_layout(void) {
  static pts_site site;
  point *p = pts_object_new(&point_type);
  computed *c = pts_object_new(&computed_type);
  p->x = 1;
  c->hidden = 7;
  CHECK(pts_method_find(p, &site, "norm1", PTS_METHOD) == (pts_function)point_norm1);
  CHECK(site.cache == (uint64_t)(uintptr_t)&point_type);
  CHECK(pts_method_find(c, &site, "norm1", PTS_METHOD) == (pts_function)computed_norm1);
  CHECK(site.cache == ((uint64_t)(uintptr_t)&computed_type | UINT64_C(2) << 48));
  CHECK(((double (*)(pts_object *))pts_method_find(c, &site, "norm1", PTS_METHOD))((pts_object *)c) == 7);
}

/* An object literal's accessor property x is listed in the field table for console.log, at its getter's closure field. */
static const pts_field literal_fields[] = {{"x", offsetof(computed, hidden), PTS_FIELD_GETTER_SETTER}};

static const pts_type literal_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Object",
    .size = sizeof(computed),
    .field_count = 1,
    .fields = literal_fields,
    .method_count = 3,
    .methods = computed_methods,
};

static void property_sites_skip_the_field_entries_of_accessor_properties(void) {
  static pts_site site;
  computed *c = pts_object_new(&literal_type);
  c->hidden = 0.5;
  CHECK(PTS_GET(double, c, &site, "x") == 5);
  CHECK(site.cache == ((uint64_t)(uintptr_t)&literal_type | PTS_SITE_ACCESSOR));
  CHECK(c->reads == 1);
}

static void property_sites_use_fields_or_accessors(void) {
  static pts_site read_site, write_site;
  point *p = pts_object_new(&point_type);
  computed *c = pts_object_new(&computed_type);
  p->x = 1.5;
  c->hidden = 0.25;
  CHECK(PTS_GET(double, c, &read_site, "x") == 2.5);
  CHECK(read_site.cache == ((uint64_t)(uintptr_t)&computed_type | PTS_SITE_ACCESSOR));
  CHECK(PTS_GET(double, p, &read_site, "x") == 1.5);
  CHECK(read_site.cache == ((uint64_t)(uintptr_t)&point_type | (uint64_t)offsetof(point, x) << 48));
  CHECK(PTS_SET(double, c, &write_site, "x", 40.0) == 40);
  CHECK(write_site.cache == ((uint64_t)(uintptr_t)&computed_type | PTS_SITE_ACCESSOR | UINT64_C(1) << 48));
  CHECK(PTS_SET(double, c, &write_site, "x", 50.0) == 50);
  CHECK(PTS_SET(double, p, &write_site, "x", 3.0) == 3);
  CHECK(c->hidden == 5 && p->x == 3);
  CHECK(c->reads == 1 && c->writes == 2);
}

/* An object holding the optional property x as its type, a pts_value that may be absent (D-065). */
typedef struct optional_x {
  const pts_type *type;
  pts_value x;
} optional_x;

static const pts_field optional_x_fields[] = {{"x", offsetof(optional_x, x), PTS_FIELD_VALUE}};

static const pts_type optional_x_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_POINTER_WORDS(1u << 1),
    .name = "Object",
    .size = sizeof(optional_x),
    .field_count = 1,
    .fields = optional_x_fields,
};

/* An optional property may be held as its type, as a number (point's x), or not at all (node has no x) (D-066). */
static void optional_sites_use_whatever_holds_the_property(void) {
  static pts_site read_site, write_site;
  optional_x *o = pts_object_new(&optional_x_type);
  point *p = pts_object_new(&point_type);
  node *n = pts_object_new(&node_type);
  p->x = 1.5;
  CHECK(pts_optional_get(o, &read_site, "x") == PTS_VALUE_ABSENT);
  CHECK(read_site.cache == ((uint64_t)(uintptr_t)&optional_x_type | (uint64_t)offsetof(optional_x, x) << 48));
  CHECK(pts_optional_get(p, &read_site, "x") == pts_value_from_number(1.5));
  CHECK(pts_optional_get(n, &read_site, "x") == PTS_VALUE_ABSENT);
  CHECK(!pts_object_has(o, "x") && pts_object_has(p, "x") && !pts_object_has(n, "x"));
  pts_optional_set(o, &write_site, "x", PTS_VALUE_UNDEFINED);
  CHECK(o->x == PTS_VALUE_UNDEFINED && pts_object_has(o, "x"));
  CHECK(write_site.cache == ((uint64_t)(uintptr_t)&optional_x_type | (uint64_t)offsetof(optional_x, x) << 48));
  pts_optional_set(o, &write_site, "x", pts_value_from_number(2));
  pts_optional_set(p, &write_site, "x", pts_value_from_number(4));
  CHECK(o->x == pts_value_from_number(2) && p->x == 4);
}

static pts_site shared_site;

/* reads_through_a_shared_site sums x over objects of both layouts through one cache. */
static void *reads_through_a_shared_site(void *arg) {
  point *p = pts_object_new(&point_type);
  tagged *t = pts_object_new(&tagged_type);
  p->x = 1;
  t->x = 2;
  double sum = 0;
  for (int i = 0; i < 100000; i++) {
    sum += *(double *)pts_field_address(i % 2 ? (void *)p : (void *)t, &shared_site, "x");
  }
  return (void *)(intptr_t)(sum == 150000 && arg == NULL);
}

static void threads_racing_on_a_site_read_the_right_fields(void) {
  pthread_t threads[THREADS_RACING];
  for (int i = 0; i < THREADS_RACING; i++) {
    CHECK(pthread_create(&threads[i], NULL, reads_through_a_shared_site, NULL) == 0);
  }
  for (int i = 0; i < THREADS_RACING; i++) {
    void *right = NULL;
    CHECK(pthread_join(threads[i], &right) == 0);
    CHECK(right == (void *)1);
  }
}

/* build_list makes a list of count nodes, each labelled with its value. */
static node *build_list(int count, int tag) {
  node *head = NULL;
  char text[32];
  for (int i = 0; i < count; i++) {
    node *n = pts_object_new(&node_type);
    n->value = tag * 1000000.0 + i;
    snprintf(text, sizeof text, "%d:%d", tag, i);
    n->label = pts_string_from_cstr(text);
    n->next = head;
    head = n;
    if (i % 1000 == 0) {
      pts_object_new(&point_type); /* garbage */
    }
  }
  return head;
}

/* list_is_intact reports whether build_list's list survived collections unchanged. */
static bool list_is_intact(const node *head, int count, int tag) {
  char text[32];
  for (int i = count - 1; i >= 0; i--, head = head->next) {
    snprintf(text, sizeof text, "%d:%d", tag, i);
    if (head == NULL || head->type != &node_type || head->value != tag * 1000000.0 + i || strcmp(head->label->bytes, text) != 0) {
      return false;
    }
  }
  return head == NULL;
}

static void collection_keeps_everything_objects_point_to(void) {
  node *list = build_list(20000, 1);
  GC_gcollect();
  for (int i = 0; i < 200000; i++) {
    pts_object_new(i % 2 ? &node_type : &point_type); /* garbage, reusing what was collected */
  }
  GC_gcollect();
  CHECK(list_is_intact(list, 20000, 1));
}

static void unreachable_objects_are_collected(void) {
  GC_gcollect();
  size_t before = GC_get_heap_size();
  for (int i = 0; i < 2000000; i++) { /* 64 MB of nodes, none kept */
    node *n = pts_object_new(&node_type);
    n->value = i;
  }
  CHECK(GC_get_heap_size() - before < 32u * 1024 * 1024);
}

enum { THREADS = 4, THREAD_NODES = 30000 };

static void *build_and_check(void *arg) {
  int tag = (int)(intptr_t)arg;
  node *list = build_list(THREAD_NODES, tag);
  if (tag == 2) {
    GC_gcollect(); /* stops the other threads while they allocate */
  }
  return (void *)(intptr_t)list_is_intact(list, THREAD_NODES, tag);
}

static void threads_allocate_while_others_collect(void) {
  pthread_t threads[THREADS];
  for (int i = 0; i < THREADS; i++) {
    CHECK(pthread_create(&threads[i], NULL, build_and_check, (void *)(intptr_t)(i + 2)) == 0);
  }
  for (int i = 0; i < THREADS; i++) {
    void *intact = NULL;
    CHECK(pthread_join(threads[i], &intact) == 0);
    CHECK(intact == (void *)1);
  }
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  bitmap_descriptors_name_words_from_the_header();
  new_objects_are_zeroed_and_typed();
  pointer_free_objects_are_allocated_atomic();
  methods_are_found_through_the_descriptor();
  instance_of_follows_the_parent_chain();
  field_addresses_are_cached_per_layout();
  method_sites_cache_the_index_per_layout();
  property_sites_use_fields_or_accessors();
  optional_sites_use_whatever_holds_the_property();
  property_sites_skip_the_field_entries_of_accessor_properties();
  threads_racing_on_a_site_read_the_right_fields();
  collection_keeps_everything_objects_point_to();
  unreachable_objects_are_collected();
  threads_allocate_while_others_collect();
  return CHECK_DONE();
}
