//go:build !darwin

package main

func offscreenOnMain(f func()) { f() }
