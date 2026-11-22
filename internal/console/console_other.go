//go:build !windows

// Package console 在非 Windows 平台上为空实现。
package console

// Attach 在非 Windows 平台上什么也不做。
func Attach() {}
