package main

// Sprite preferences retain their canonical dimensions. Text remains legible
// even when the pet is set to 50%; the activity region never scales with sprites.
func petActivityWindowSize(width, height int) (int, int) {
	return max(width, 320), height + 88
}
