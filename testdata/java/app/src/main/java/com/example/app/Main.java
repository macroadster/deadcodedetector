package com.example.app;

import com.example.app.Used;
import com.example.app.UnusedExport;
import java.util.List;

public class Main {
    private int usedField = 1;
    private int unusedField = 2;

    public static void main(String[] args) {
        System.out.println(Used.ok() + new Main().live());
        Side.touch();
    }

    private int live() {
        return this.usedField;
    }

    private static int unusedPrivate() {
        return 0;
    }

    // dcd:ignore
    private static int ignoredDead() {
        return -1;
    }
}
