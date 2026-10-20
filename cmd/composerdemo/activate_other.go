//go:build !darwin

package main

import "gonativeui/app"

func activate(*app.App) {}

func keyWindow(*app.App) bool { return true }
