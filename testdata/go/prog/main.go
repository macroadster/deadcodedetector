package main

import "fmt"

func main() {
	var g Greeter
	g = Helloer{}
	g.Greet()
	used()
}

func used() { fmt.Println("used") }

func dead() { fmt.Println("dead") }

type Greeter interface{ Greet() }

type Helloer struct{}
type Goodbyer struct{}

var _ Greeter = Helloer{}
var _ Greeter = Goodbyer{}

func (Helloer) Greet()  { hello() }
func (Goodbyer) Greet() { goodbye() }

func hello()   { fmt.Println("hello") }
func goodbye() { fmt.Println("goodbye") }

type leftover struct{ n int }

const deadConst = 1

var deadVar int
