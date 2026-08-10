package lib

// Public is part of the library API.
func Public() { helper() }

func helper() {}

func unusedHelper() {}

// dcd:ignore
func ignoredDead() {}
