/* Objects: type descriptors, allocation by layout, and collection, also from several threads. */
#include "pts.h"

#include "check.h"

#include <gc_inline.h> /* GC_I_PTRFREE */
#include <pthread.h>
#include <stdio.h>

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

static double point_norm1(point *p) { return p->x + p->y; }

static const pts_field point_fields[] = {{"x", offsetof(point, x), PTS_FIELD_NUMBER}, {"y", offsetof(point, y), PTS_FIELD_NUMBER}};

static const pts_method point_methods[] = {{"norm1", (void (*)(void))point_norm1}};

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
  double (*norm1)(point *) = (double (*)(point *))point_type.methods[0].function;
  CHECK(norm1(p) == 3.5);
  labelled_point *lp = pts_object_new(&labelled_point_type);
  CHECK(lp->base.type->parent == &point_type);
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
  collection_keeps_everything_objects_point_to();
  unreachable_objects_are_collected();
  threads_allocate_while_others_collect();
  return CHECK_DONE();
}
