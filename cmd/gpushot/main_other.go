//go:build !darwin

// Command gpushot renders a renderer showcase to a PNG; macOS only.
package main

import "fmt"

func main() { fmt.Println("darwin only") }
