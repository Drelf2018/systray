//go:build !windows

package systray

// #include "systray.h"
import "C"

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"unsafe"
)

func registerSystray() {
	C.registerSystray()
}

func nativeLoop() {
	C.nativeLoop()
}

func quit() {
	C.quit()
}

// SetIcon sets the systray icon.
// iconBytes should be the content of .ico for windows and .ico/.jpg/.png
// for other platforms.
func SetIcon(iconBytes []byte) {
	cstr := (*C.char)(unsafe.Pointer(&iconBytes[0]))
	C.setIcon(cstr, (C.int)(len(iconBytes)), false)
}

// SetTitle sets the systray title, only available on Mac and Linux.
func SetTitle(title string) {
	C.setTitle(C.CString(title))
}

// SetTooltip sets the systray tooltip to display on mouse hover of the tray icon,
// only available on Mac and Windows.
func SetTooltip(tooltip string) {
	C.setTooltip(C.CString(tooltip))
}

// SetOnLeftClick sets a callback to be invoked when the tray icon is left-clicked.
// This is only supported on Windows; on macOS and Linux it does nothing.
func SetOnLeftClick(callback func()) {
	// do nothing
}

func addOrUpdateMenuItem(item *MenuItem) {
	var disabled C.short
	if item.disabled {
		disabled = 1
	}
	var checked C.short
	if item.checked {
		checked = 1
	}
	var isCheckable C.short
	if item.isCheckable {
		isCheckable = 1
	}
	var parentID uint32 = 0
	if item.parent != nil {
		parentID = item.parent.id
	}
	C.add_or_update_menu_item(
		C.int(item.id),
		C.int(parentID),
		C.CString(item.title),
		C.CString(item.tooltip),
		disabled,
		checked,
		isCheckable,
	)
}

func addSeparator(id uint32) {
	C.add_separator(C.int(id))
}

func hideMenuItem(item *MenuItem) {
	C.hide_menu_item(
		C.int(item.id),
	)
}

func showMenuItem(item *MenuItem) {
	C.show_menu_item(
		C.int(item.id),
	)
}

//export systray_ready
func systray_ready() {
	systrayReady()
}

//export systray_on_exit
func systray_on_exit() {
	systrayExit()
}

//export systray_menu_item_selected
func systray_menu_item_selected(cID C.int) {
	systrayMenuItemSelected(uint32(cID))
}

// iconSize is the edge, in pixels, at which a vector icon is rasterized.
//
// Neither back-end needs a number from us: systray_darwin.m hands the image to
// AppKit as 16pt and lets it scale, and on Linux the bytes reach
// app_indicator_set_icon_full, which gdk-pixbuf scales to the panel. 32 covers a
// 16pt icon at 2x.
func iconSize() int { return 32 }

// isNativeIconFormat reports whether data of the given format can be handed to the
// native back-end as it is. PNG, JPEG and ICO all can.
func isNativeIconFormat(format string) bool {
	switch format {
	case "png", "jpeg", "ico":
		return true
	}
	return false
}

// encodeIcon encodes img as PNG, the format every supported back-end reads.
func encodeIcon(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("convert to .png: %w", err)
	}
	return buf.Bytes(), nil
}
