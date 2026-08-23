#include "util.h"

int used_fn(void) {
    return USED_MACRO;
}

/* Sibling of used_fn: never called, but reads as production logic. */
int unused_classify(int n) {
    if (n < 0) {
        return used_fn();
    }
    switch (n) {
    case 0:
        return USED_MACRO;
    case 1:
        return used_fn();
    default:
        return n % 2 == 0 ? n : -n;
    }
}

/* unused_scale is only named from unused_cb, which is never read. */
static int unused_scale(int n) {
    if (n <= 0) {
        return 0;
    }
    return n % 2 == 0 ? n / 2 : n * 2;
}

static int (*unused_cb)(int) = unused_scale;
