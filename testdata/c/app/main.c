#include "util.h"
#include <dlfcn.h>

int main(void) {
    void *h = dlopen("./libplugin.so", 1);
    void *fn = dlsym(h, "plugin_init");
    (void)h;
    (void)fn;
    return used_fn();
}

/* Never called. Looks live: branches and calls used_fn / USED_MACRO. */
static int unused_static(int n) {
    if (n <= 0) {
        return used_fn();
    }
    while (n > 0) {
        if (n == USED_MACRO) {
            return n;
        }
        n--;
    }
    return 0;
}
