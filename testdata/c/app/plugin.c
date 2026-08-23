int plugin_init(void) {
    return 1;
}

int plugin_unused(int cmd, int flags) {
    switch (cmd) {
    case 1:
        if (flags & 1) {
            return 2;
        }
        return 3;
    case 2:
        return flags ? 4 : 5;
    default:
        return -1;
    }
}
