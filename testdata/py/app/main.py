from used import used, never_used
from unused_export import leftover
from pkg import util
import side
import os

def live():
    return used() + util.helper()

def local_dead():
    return 0

if __name__ == "__main__":
    live()
