#include "util.h"
#include <dlfcn.h>

int main(void) {
    void *h = dlopen("./libplugin.so", 1);
    void *fn = dlsym(h, "plugin_init");
    (void)h;
    (void)fn;
    return used_fn();
}

static int unused_static(void) {
    return 0;
}
