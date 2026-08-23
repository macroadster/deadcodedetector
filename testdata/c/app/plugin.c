int plugin_init(void) {
    return 1;
}

int plugin_unused(int flags) {
    if (flags & 1) {
        return 2;
    }
    if (flags & 2) {
        return 3;
    }
    return flags ? 4 : 5;
}
