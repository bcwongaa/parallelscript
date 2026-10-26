#include <gc.h>
#include <stdio.h>
#include <stdlib.h>

int __real_uv_resident_set_memory(size_t *rss);

/* Another worker can grow the collector between the heap counters and the OS's RSS
 * sample. Place real, locked collector growth at that boundary without a timing race. */
int __wrap_uv_resident_set_memory(size_t *rss) {
  static int expanded;
  if (!expanded) {
    expanded = 1;
    GC_word heap_size;
    GC_get_heap_usage_safe(&heap_size, NULL, NULL, NULL, NULL);
    const size_t growth = (size_t)heap_size + 8 * 1024 * 1024;
    if (!GC_expand_hp(growth)) {
      fputs("test collector growth failed\n", stderr);
      abort();
    }
    fprintf(stderr, "test grew collector by %zu bytes at RSS sample\n", growth);
  }
  return __real_uv_resident_set_memory(rss);
}
