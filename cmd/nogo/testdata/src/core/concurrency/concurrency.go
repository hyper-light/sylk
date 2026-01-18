package concurrency

func SpawnGoroutine() {
	go func() {
		println("allowed in core/concurrency")
	}()
}

func SpawnNamedGoroutine() {
	go worker()
}

func worker() {
	println("worker")
}
