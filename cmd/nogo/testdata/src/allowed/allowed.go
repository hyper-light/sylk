package allowed

func TestNoGoStatements() {
	println("no goroutines here")
}

func TestRegularFunction() {
	result := add(1, 2)
	println(result)
}

func add(a, b int) int {
	return a + b
}
