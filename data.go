package main

// sampleTasks returns the tasks used in demo mode.
func sampleTasks() []Task {
	return []Task{
		newTask(todo, "buy milk", "strawberry milk"),
		newTask(todo, "eat sushi", "negitoro roll, miso soup, rice"),
		newTask(todo, "fold laundry", "or wear wrinkly t-shirts"),
		newTask(inProgress, "write code", "don't worry, it's Go"),
		newTask(done, "stay cool", "as a cucumber"),
	}
}
