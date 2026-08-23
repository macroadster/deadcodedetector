int orphan_fn(int x) {
    if (x < 0) {
        return -1;
    }
    for (; x > 0; x--) {
        if (x == 2) {
            return x;
        }
    }
    return 0;
}
