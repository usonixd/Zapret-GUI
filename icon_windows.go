//go:build windows

package main

import (
	"unsafe"
)

var loadIconImage = u32.NewProc("LoadImageW")

const WM_SETICON = 0x0080

func appIconLarge() uintptr { return call(loadIconImage, hinst, 1, 1, 32, 32, 0) }
func appIconSmall() uintptr { return call(loadIconImage, hinst, 1, 1, 16, 16, 0) }
func setAppWindowIcons(h uintptr) {
	if h == 0 {
		return
	}
	if icon := appIconLarge(); icon != 0 {
		send(h, WM_SETICON, 1, icon)
	}
	if icon := appIconSmall(); icon != 0 {
		send(h, WM_SETICON, 0, icon)
	}
}

var _ = unsafe.Pointer(nil)
