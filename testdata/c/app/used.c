#include "util.h"

int used_fn(void) {
    return USED_MACRO;
}

/* Only called from unused_classify. */
static int unused_rank(int n) {
    if (n < 2) {
        return n;
    }
    if (n % 2 == 0) {
        return unused_rank(n / 2);
    }
    return unused_rank(3 * n + 1);
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
        return unused_rank(n);
    default:
        return n % 2 == 0 ? unused_rank(n) : -n;
    }
}

/* Only named from the unused_cb table. */
static int unused_scale(int n) {
    if (n <= 0) {
        return 0;
    }
    return n % 2 == 0 ? n / 2 : n * 2;
}

static int (*const unused_cb[])(int) = {
    unused_scale,
};
