// Package output provides consistent user-facing messages for container operations.
package output

import "fmt"

// PullingImage displays a message indicating an image is being pulled.
func PullingImage(imageName string) {
	fmt.Printf("Pulling image %s...\n", imageName)
}

// BuildingImage displays a message indicating an image is being built.
func BuildingImage(tag string) {
	fmt.Printf("Building image %s...\n", tag)
}
