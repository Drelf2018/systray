//go:build !windows

package systray

// #include "systray.h"
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"unsafe"
)

// The back-ends take ownership of every string handed to them as a char*, and
// free it once they have copied what they need: setTitle and setTooltip free at
// once, in both systray_darwin.m and systray_linux.c, and
// add_or_update_menu_item passes its two strings to the GTK main loop, which
// frees them when it consumes the item it queued (do_add_or_update_menu_item).
// The C.CString calls below are deliberately left alone: freeing them here as
// well would be a double free.

func registerSystray() {
	C.registerSystray()
}

func nativeLoop() {
	C.nativeLoop()
}

func quit() {
	C.quit()
}

// errEmptyIcon is what the back-ends report when they are handed an icon of no
// bytes.
var errEmptyIcon = errors.New("icon: empty")

// checkIcon reports whether iconBytes are worth handing to the back-end. An
// empty slice would panic on the address of its first element, and a nil
// pointer passed in its place would clear the icon already on screen, so the
// call is dropped with a log line instead.
func checkIcon(iconBytes []byte) error {
	if len(iconBytes) == 0 {
		return errEmptyIcon
	}
	return nil
}

// SetIcon sets the systray icon.
// iconBytes should be the content of .ico for windows and .ico/.jpg/.png
// for other platforms. An empty icon is ignored.
func SetIcon(iconBytes []byte) {
	if err := checkIcon(iconBytes); err != nil {
		logError("unable to set icon", "error", err)
		return
	}
	cstr := (*C.char)(unsafe.Pointer(&iconBytes[0]))
	C.setIcon(cstr, (C.int)(len(iconBytes)), false)
}

// SetTitle sets the systray title, only available on Mac and Linux.
func SetTitle(title string) {
	C.setTitle(C.CString(title)) // the back-end frees this
}

// SetTooltip sets the systray tooltip to display on mouse hover of the tray icon,
// only available on Mac and Windows.
func SetTooltip(tooltip string) {
	C.setTooltip(C.CString(tooltip)) // the back-end frees this
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
	// Both strings belong to the back-end from here; it frees them.
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

// EnableDPIAwareness is a no-op outside Windows: scaled displays are the
// platform's own business there, and systray reads no desktop metric.
func EnableDPIAwareness() error { return nil }
