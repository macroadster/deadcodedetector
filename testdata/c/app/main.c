#include "util.h"
#include <dlfcn.h>

int main(void) {
    void *h = dlopen("./libplugin.so", 1);
    void *fn = dlsym(h, "plugin_init");
    (void)h;
    (void)fn;
    return used_fn();
}

/* Never called from outside; the recursive call is not an external use. */
static int unused_static(int n) {
    if (n <= 0) {
        return used_fn();
    }
    if (n == USED_MACRO) {
        return n;
    }
    return unused_static(n - 1);
}
