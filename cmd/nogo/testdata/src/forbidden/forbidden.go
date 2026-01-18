package forbidden

func TestForbiddenGoFunc() {
	go func() { // want "raw 'go' statement forbidden - use scope.Go\\(\\) from core/concurrency"
		println("inline goroutine")
	}()
}

func TestForbiddenGoNamedFunc() {
	go someFunc() // want "raw 'go' statement forbidden - use scope.Go\\(\\) from core/concurrency"
}

func someFunc() {
	println("named function")
}

func TestForbiddenGoMethodCall() {
	s := &service{}
	go s.run() // want "raw 'go' statement forbidden - use scope.Go\\(\\) from core/concurrency"
}

type service struct{}

func (s *service) run() {}
