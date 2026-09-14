package systray

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	ico "github.com/biessek/golang-ico"
	"golang.org/x/sys/windows"
)

// System metrics and DrawIconEx flags used to size and draw the tray icon.
const (
	// SM_CXSMICON and SM_CYSMICON are the small-icon metrics, which is what the
	// notification area draws. They follow the display scale factor, so they have
	// to be read rather than assumed.
	SM_CXSMICON = 49
	SM_CYSMICON = 50

	// DI_NORMAL draws the icon together with its mask.
	DI_NORMAL = 0x3
)

// Shell_NotifyIcon
const (
	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
)

// Window messages
const (
	WM_USER       = 0x0400
	WM_COMMAND    = 0x0111
	WM_ENDSESSION = 0x0016
	WM_CLOSE      = 0x0010
	WM_DESTROY    = 0x0002
	WM_LBUTTONUP  = 0x0202
	WM_RBUTTONUP  = 0x0205
)

// Styles and identifiers for the hidden window the tray lives in
const (
	IDI_APPLICATION = 32512
	IDC_ARROW       = 32512 // Standard arrow
	SW_HIDE         = 0
	CW_USEDEFAULT   = 0x80000000
	CS_HREDRAW      = 0x0002
	CS_VREDRAW      = 0x0001

	WS_OVERLAPPED       = 0x00000000
	WS_CAPTION          = 0x00C00000
	WS_MAXIMIZEBOX      = 0x00010000
	WS_MINIMIZEBOX      = 0x00020000
	WS_SYSMENU          = 0x00080000
	WS_THICKFRAME       = 0x00040000
	WS_OVERLAPPEDWINDOW = WS_OVERLAPPED
)

// Menus
const (
	MIM_APPLYTOSUBMENUS = 0x80000000 // Settings apply to the menu and all of its submenus

	MIIM_STATE   = 0x00000001
	MIIM_ID      = 0x00000002
	MIIM_SUBMENU = 0x00000004
	MIIM_STRING  = 0x00000040
	MIIM_BITMAP  = 0x00000080
	MIIM_FTYPE   = 0x00000100

	MFT_STRING    = 0x00000000
	MFT_SEPARATOR = 0x00000800

	MFS_CHECKED  = 0x00000008
	MFS_DISABLED = 0x00000003

	MF_BYCOMMAND = 0x00000000

	TPM_LEFTALIGN   = 0x0000
	TPM_BOTTOMALIGN = 0x0020
)

const (
	biRGB        = 0 // BI_RGB: uncompressed pixels, so alpha travels as part of them
	dibRGBColors = 0 // DIB_RGB_COLORS
)

// Helpful sources: https://github.com/golang/exp/blob/master/shiny/driver/internal/win32

var (
	g32                 = windows.NewLazySystemDLL("Gdi32.dll")
	pCreateCompatibleDC = g32.NewProc("CreateCompatibleDC")
	pDeleteDC           = g32.NewProc("DeleteDC")
	pSelectObject       = g32.NewProc("SelectObject")
	pCreateDIBSection   = g32.NewProc("CreateDIBSection")
	pDeleteObject       = g32.NewProc("DeleteObject")
	pGetDIBits          = g32.NewProc("GetDIBits")
	pGetObject          = g32.NewProc("GetObjectW")

	k32              = windows.NewLazySystemDLL("Kernel32.dll")
	pGetModuleHandle = k32.NewProc("GetModuleHandleW")

	s32              = windows.NewLazySystemDLL("Shell32.dll")
	pShellNotifyIcon = s32.NewProc("Shell_NotifyIconW")

	u32                       = windows.NewLazySystemDLL("User32.dll")
	pCreateMenu               = u32.NewProc("CreateMenu")
	pCreatePopupMenu          = u32.NewProc("CreatePopupMenu")
	pCreateWindowEx           = u32.NewProc("CreateWindowExW")
	pDefWindowProc            = u32.NewProc("DefWindowProcW")
	pRemoveMenu               = u32.NewProc("RemoveMenu")
	pDestroyWindow            = u32.NewProc("DestroyWindow")
	pDispatchMessage          = u32.NewProc("DispatchMessageW")
	pDrawIconEx               = u32.NewProc("DrawIconEx")
	pGetIconInfo              = u32.NewProc("GetIconInfo")
	pGetCursorPos             = u32.NewProc("GetCursorPos")
	pGetDC                    = u32.NewProc("GetDC")
	pGetMessage               = u32.NewProc("GetMessageW")
	pGetSystemMetrics         = u32.NewProc("GetSystemMetrics")
	pInsertMenuItem           = u32.NewProc("InsertMenuItemW")
	pLoadCursor               = u32.NewProc("LoadCursorW")
	pLoadIcon                 = u32.NewProc("LoadIconW")
	pCreateIconFromResourceEx = u32.NewProc("CreateIconFromResourceEx")
	pPostMessage              = u32.NewProc("PostMessageW")
	pPostQuitMessage          = u32.NewProc("PostQuitMessage")
	pRegisterClass            = u32.NewProc("RegisterClassExW")
	pRegisterWindowMessage    = u32.NewProc("RegisterWindowMessageW")
	pReleaseDC                = u32.NewProc("ReleaseDC")
	pSetForegroundWindow      = u32.NewProc("SetForegroundWindow")
	pSetMenuInfo              = u32.NewProc("SetMenuInfo")
	pSetMenuItemInfo          = u32.NewProc("SetMenuItemInfoW")
	pShowWindow               = u32.NewProc("ShowWindow")
	pTrackPopupMenu           = u32.NewProc("TrackPopupMenu")
	pTranslateMessage         = u32.NewProc("TranslateMessage")
	pUnregisterClass          = u32.NewProc("UnregisterClassW")
	pUpdateWindow             = u32.NewProc("UpdateWindow")
)

// Contains window class information.
// It is used with the RegisterClassEx and GetClassInfoEx functions.
// https://msdn.microsoft.com/en-us/library/ms633577.aspx
type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background windows.Handle
	MenuName, ClassName                *uint16
	IconSm                             windows.Handle
}

// Registers a window class for subsequent use in calls to the CreateWindow or CreateWindowEx function.
// https://msdn.microsoft.com/en-us/library/ms633587.aspx
func (w *wndClassEx) register() error {
	w.Size = uint32(unsafe.Sizeof(*w))
	res, _, err := pRegisterClass.Call(uintptr(unsafe.Pointer(w)))
	if res == 0 {
		return err
	}
	return nil
}

// Unregisters a window class, freeing the memory required for the class.
// https://msdn.microsoft.com/en-us/library/ms644899.aspx
func (w *wndClassEx) unregister() error {
	res, _, err := pUnregisterClass.Call(
		uintptr(unsafe.Pointer(w.ClassName)),
		uintptr(w.Instance),
	)
	if res == 0 {
		return err
	}
	return nil
}

// Contains information that the system needs to display notifications in the notification area.
// Used by Shell_NotifyIcon.
// https://msdn.microsoft.com/en-us/library/windows/desktop/bb773352(v=vs.85).aspx
// https://msdn.microsoft.com/en-us/library/windows/desktop/bb762159
type notifyIconData struct {
	Size                       uint32
	Wnd                        windows.Handle
	ID, Flags, CallbackMessage uint32
	Icon                       windows.Handle
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	Timeout, Version           uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	GuidItem                   windows.GUID
	BalloonIcon                windows.Handle
}

func (nid *notifyIconData) add() error {
	res, _, err := pShellNotifyIcon.Call(
		uintptr(NIM_ADD),
		uintptr(unsafe.Pointer(nid)),
	)
	if res == 0 {
		return err
	}
	return nil
}

func (nid *notifyIconData) modify() error {
	res, _, err := pShellNotifyIcon.Call(
		uintptr(NIM_MODIFY),
		uintptr(unsafe.Pointer(nid)),
	)
	if res == 0 {
		return err
	}
	return nil
}

func (nid *notifyIconData) delete() error {
	res, _, err := pShellNotifyIcon.Call(
		uintptr(NIM_DELETE),
		uintptr(unsafe.Pointer(nid)),
	)
	if res == 0 {
		return err
	}
	return nil
}

// Contains information about a menu item.
// https://msdn.microsoft.com/en-us/library/windows/desktop/ms647578(v=vs.85).aspx
type menuItemInfo struct {
	Size, Mask, Type, State     uint32
	ID                          uint32
	SubMenu, Checked, Unchecked windows.Handle
	ItemData                    uintptr
	TypeData                    *uint16
	Cch                         uint32
	BMPItem                     windows.Handle
}

// The POINT structure defines the x- and y- coordinates of a point.
// https://msdn.microsoft.com/en-us/library/windows/desktop/dd162805(v=vs.85).aspx
type point struct {
	X, Y int32
}

// Contains information about loaded resources
type winTray struct {
	instance,
	icon,
	cursor,
	window windows.Handle

	loadedImages   map[string]windows.Handle
	muLoadedImages sync.RWMutex
	// menus keeps track of the submenus keyed by the menu item ID, plus 0
	// which corresponds to the main popup menu.
	menus   map[uint32]windows.Handle
	muMenus sync.RWMutex
	// menuOf keeps track of the menu each menu item belongs to.
	menuOf   map[uint32]windows.Handle
	muMenuOf sync.RWMutex
	// menuItemIcons maintains the bitmap of each menu item (if applies). It's
	// needed to show the icon correctly when showing a previously hidden menu
	// item again.
	menuItemIcons   map[uint32]windows.Handle
	muMenuItemIcons sync.RWMutex
	visibleItems    map[uint32][]uint32
	muVisibleItems  sync.RWMutex

	nid   *notifyIconData
	muNID sync.RWMutex
	wcex  *wndClassEx

	wmSystrayMessage,
	wmTaskbarCreated uint32
}

// Shows the given .ico bytes in the notification area.
// Shell_NotifyIcon: https://msdn.microsoft.com/en-us/library/windows/desktop/bb762159(v=vs.85).aspx
func (t *winTray) setIcon(iconBytes []byte) error {
	h, err := t.iconFromBytes(iconBytes)
	if err != nil {
		return err
	}

	t.muNID.Lock()
	defer t.muNID.Unlock()
	t.nid.Icon = h
	t.nid.Flags |= NIF_ICON
	t.nid.Size = uint32(unsafe.Sizeof(*t.nid))

	return t.nid.modify()
}

// Sets tooltip on icon.
// Shell_NotifyIcon: https://msdn.microsoft.com/en-us/library/windows/desktop/bb762159(v=vs.85).aspx
func (t *winTray) setTooltip(src string) error {
	b, err := windows.UTF16FromString(src)
	if err != nil {
		return err
	}

	t.muNID.Lock()
	defer t.muNID.Unlock()
	copy(t.nid.Tip[:], b[:])
	t.nid.Flags |= NIF_TIP
	t.nid.Size = uint32(unsafe.Sizeof(*t.nid))

	return t.nid.modify()
}

var wt winTray

var (
	// onLeftClick is the callback invoked when the tray icon is left-clicked.
	// Only Windows makes use of it; the other platforms ignore it.
	onLeftClick   func()
	muOnLeftClick sync.RWMutex
)

// WindowProc callback function that processes messages sent to a window.
// https://msdn.microsoft.com/en-us/library/windows/desktop/ms633573(v=vs.85).aspx
func (t *winTray) wndProc(hWnd windows.Handle, message uint32, wParam, lParam uintptr) (lResult uintptr) {
	switch message {
	case WM_COMMAND:
		menuItemId := int32(wParam)
		// https://docs.microsoft.com/en-us/windows/win32/menurc/wm-command#menus
		if menuItemId != -1 {
			systrayMenuItemSelected(uint32(wParam))
		}
	case WM_CLOSE:
		pDestroyWindow.Call(uintptr(t.window))
		t.wcex.unregister()
	case WM_DESTROY:
		// same as WM_ENDSESSION, but throws 0 exit code after all
		defer pPostQuitMessage.Call(uintptr(int32(0)))
		fallthrough
	case WM_ENDSESSION:
		t.muNID.Lock()
		if t.nid != nil {
			t.nid.delete()
		}
		t.muNID.Unlock()
		systrayExit()
	case t.wmSystrayMessage:
		switch lParam {
		case WM_RBUTTONUP:
			t.showMenu()
		case WM_LBUTTONUP:
			muOnLeftClick.RLock()
			fn := onLeftClick
			muOnLeftClick.RUnlock()
			if fn != nil {
				fn()
			} else {
				t.showMenu()
			}
		}
	case t.wmTaskbarCreated: // on explorer.exe restarts
		t.muNID.Lock()
		t.nid.add()
		t.muNID.Unlock()
	default:
		// Calls the default window procedure to provide default processing for any window messages that an application does not process.
		// https://msdn.microsoft.com/en-us/library/windows/desktop/ms633572(v=vs.85).aspx
		lResult, _, _ = pDefWindowProc.Call(
			uintptr(hWnd),
			uintptr(message),
			uintptr(wParam),
			uintptr(lParam),
		)
	}
	return
}

func (t *winTray) initInstance() error {
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms633548(v=vs.85).aspx
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms632600(v=vs.85).aspx
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ff729176

	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms644931(v=vs.85).aspx

	t.wmSystrayMessage = WM_USER + 1
	t.visibleItems = make(map[uint32][]uint32)
	t.menus = make(map[uint32]windows.Handle)
	t.menuOf = make(map[uint32]windows.Handle)
	t.menuItemIcons = make(map[uint32]windows.Handle)

	taskbarEventNamePtr, _ := windows.UTF16PtrFromString("TaskbarCreated")
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms644947
	res, _, err := pRegisterWindowMessage.Call(
		uintptr(unsafe.Pointer(taskbarEventNamePtr)),
	)
	if res == 0 {
		return callError(err, "RegisterWindowMessage(TaskbarCreated) failed")
	}
	t.wmTaskbarCreated = uint32(res)

	t.loadedImages = make(map[string]windows.Handle)

	instanceHandle, _, err := pGetModuleHandle.Call(0)
	if instanceHandle == 0 {
		return err
	}
	t.instance = windows.Handle(instanceHandle)

	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms648072(v=vs.85).aspx
	iconHandle, _, err := pLoadIcon.Call(0, uintptr(IDI_APPLICATION))
	if iconHandle == 0 {
		return err
	}
	t.icon = windows.Handle(iconHandle)

	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms648391(v=vs.85).aspx
	cursorHandle, _, err := pLoadCursor.Call(0, uintptr(IDC_ARROW))
	if cursorHandle == 0 {
		return err
	}
	t.cursor = windows.Handle(cursorHandle)

	classNamePtr, err := windows.UTF16PtrFromString("SystrayClass")
	if err != nil {
		return err
	}

	windowNamePtr, err := windows.UTF16PtrFromString("") // never shown, so it carries no title
	if err != nil {
		return err
	}

	t.wcex = &wndClassEx{
		Style:      CS_HREDRAW | CS_VREDRAW,
		WndProc:    windows.NewCallback(t.wndProc),
		Instance:   t.instance,
		Icon:       t.icon,
		Cursor:     t.cursor,
		Background: windows.Handle(6), // (COLOR_WINDOW + 1)
		ClassName:  classNamePtr,
		IconSm:     t.icon,
	}
	if err := t.wcex.register(); err != nil {
		return err
	}

	windowHandle, _, err := pCreateWindowEx.Call(
		uintptr(0),
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(windowNamePtr)),
		uintptr(WS_OVERLAPPEDWINDOW),
		uintptr(CW_USEDEFAULT),
		uintptr(CW_USEDEFAULT),
		uintptr(CW_USEDEFAULT),
		uintptr(CW_USEDEFAULT),
		uintptr(0),
		uintptr(0),
		uintptr(t.instance),
		uintptr(0),
	)
	if windowHandle == 0 {
		return err
	}
	t.window = windows.Handle(windowHandle)

	pShowWindow.Call(
		uintptr(t.window),
		uintptr(SW_HIDE),
	)

	pUpdateWindow.Call(
		uintptr(t.window),
	)

	t.muNID.Lock()
	defer t.muNID.Unlock()
	t.nid = &notifyIconData{
		Wnd:             windows.Handle(t.window),
		ID:              100,
		Flags:           NIF_MESSAGE,
		CallbackMessage: t.wmSystrayMessage,
	}
	t.nid.Size = uint32(unsafe.Sizeof(*t.nid))

	return t.nid.add()
}

func (t *winTray) createMenu() error {

	menuHandle, _, err := pCreatePopupMenu.Call()
	if menuHandle == 0 {
		return err
	}
	t.menus[0] = windows.Handle(menuHandle)

	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms647575(v=vs.85).aspx
	mi := struct {
		Size, Mask, Style, Max uint32
		Background             windows.Handle
		ContextHelpID          uint32
		MenuData               uintptr
	}{
		Mask: MIM_APPLYTOSUBMENUS,
	}
	mi.Size = uint32(unsafe.Sizeof(mi))

	res, _, err := pSetMenuInfo.Call(
		uintptr(t.menus[0]),
		uintptr(unsafe.Pointer(&mi)),
	)
	if res == 0 {
		return err
	}
	return nil
}

func (t *winTray) convertToSubMenu(menuItemId uint32) (windows.Handle, error) {

	res, _, err := pCreateMenu.Call()
	if res == 0 {
		return 0, err
	}
	menu := windows.Handle(res)

	mi := menuItemInfo{Mask: MIIM_SUBMENU, SubMenu: menu}
	mi.Size = uint32(unsafe.Sizeof(mi))
	t.muMenuOf.RLock()
	hMenu := t.menuOf[menuItemId]
	t.muMenuOf.RUnlock()
	res, _, err = pSetMenuItemInfo.Call(
		uintptr(hMenu),
		uintptr(menuItemId),
		0,
		uintptr(unsafe.Pointer(&mi)),
	)
	if res == 0 {
		return 0, err
	}
	t.muMenus.Lock()
	t.menus[menuItemId] = menu
	t.muMenus.Unlock()
	return menu, nil
}

func (t *winTray) addOrUpdateMenuItem(menuItemId uint32, parentId uint32, title string, disabled, checked bool) error {
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms647578(v=vs.85).aspx
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return err
	}

	mi := menuItemInfo{
		Mask:     MIIM_FTYPE | MIIM_STRING | MIIM_ID | MIIM_STATE,
		Type:     MFT_STRING,
		ID:       uint32(menuItemId),
		TypeData: titlePtr,
		Cch:      uint32(len(title)),
	}
	mi.Size = uint32(unsafe.Sizeof(mi))
	if disabled {
		mi.State |= MFS_DISABLED
	}
	if checked {
		mi.State |= MFS_CHECKED
	}
	t.muMenuItemIcons.RLock()
	hIcon := t.menuItemIcons[menuItemId]
	t.muMenuItemIcons.RUnlock()
	if hIcon > 0 {
		mi.Mask |= MIIM_BITMAP
		mi.BMPItem = hIcon
	}

	var res uintptr
	t.muMenus.RLock()
	menu, exists := t.menus[parentId]
	t.muMenus.RUnlock()
	if !exists {
		menu, err = t.convertToSubMenu(parentId)
		if err != nil {
			return err
		}
		t.muMenus.Lock()
		t.menus[parentId] = menu
		t.muMenus.Unlock()
	} else if t.getVisibleItemIndex(parentId, menuItemId) != -1 {
		// We set the menu item info based on the menuID
		res, _, err = pSetMenuItemInfo.Call(
			uintptr(menu),
			uintptr(menuItemId),
			0,
			uintptr(unsafe.Pointer(&mi)),
		)
		// A failed update is not a missing item: report it here rather than
		// fall through to the branch below, which would try to create it.
		if res == 0 {
			return callError(err, fmt.Sprintf("update menu item %d: SetMenuItemInfo failed", menuItemId))
		}
	}

	if res == 0 {
		// Menu item does not already exist, create it
		t.muMenus.RLock()
		submenu, exists := t.menus[menuItemId]
		t.muMenus.RUnlock()
		if exists {
			mi.Mask |= MIIM_SUBMENU
			mi.SubMenu = submenu
		}
		t.addToVisibleItems(parentId, menuItemId)
		position := t.getVisibleItemIndex(parentId, menuItemId)
		res, _, err = pInsertMenuItem.Call(
			uintptr(menu),
			uintptr(position),
			1,
			uintptr(unsafe.Pointer(&mi)),
		)
		if res == 0 {
			t.delFromVisibleItems(parentId, menuItemId)
			return err
		}
		t.muMenuOf.Lock()
		t.menuOf[menuItemId] = menu
		t.muMenuOf.Unlock()
	}

	return nil
}

func (t *winTray) addSeparatorMenuItem(menuItemId, parentId uint32) error {
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms647578(v=vs.85).aspx

	mi := menuItemInfo{
		Mask: MIIM_FTYPE | MIIM_ID | MIIM_STATE,
		Type: MFT_SEPARATOR,
		ID:   uint32(menuItemId),
	}

	mi.Size = uint32(unsafe.Sizeof(mi))

	t.addToVisibleItems(parentId, menuItemId)
	position := t.getVisibleItemIndex(parentId, menuItemId)
	t.muMenus.RLock()
	menu := uintptr(t.menus[parentId])
	t.muMenus.RUnlock()
	res, _, err := pInsertMenuItem.Call(
		menu,
		uintptr(position),
		1,
		uintptr(unsafe.Pointer(&mi)),
	)
	if res == 0 {
		return err
	}

	return nil
}

// callError reports a failed Windows call. Not every failing call sets the last
// error: a zero errno only means Windows left the value untouched, and reporting
// it as-is would read "The operation completed successfully".
func callError(err error, fallback string) error {
	if errno, ok := err.(syscall.Errno); ok && errno != 0 {
		return err
	}
	return errors.New("systray: " + fallback)
}

func (t *winTray) hideMenuItem(menuItemId, parentId uint32) error {
	// https://docs.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-removemenu

	t.muMenus.RLock()
	menu := uintptr(t.menus[parentId])
	t.muMenus.RUnlock()
	res, _, err := pRemoveMenu.Call(
		menu,
		uintptr(menuItemId),
		MF_BYCOMMAND,
	)
	if res == 0 {
		return callError(err, fmt.Sprintf("hide menu item %d: no such item", menuItemId))
	}
	t.delFromVisibleItems(parentId, menuItemId)

	return nil
}

func (t *winTray) showMenu() error {
	p := point{}
	res, _, err := pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if res == 0 {
		return err
	}
	pSetForegroundWindow.Call(uintptr(t.window))

	res, _, err = pTrackPopupMenu.Call(
		uintptr(t.menus[0]),
		TPM_BOTTOMALIGN|TPM_LEFTALIGN,
		uintptr(p.X),
		uintptr(p.Y),
		0,
		uintptr(t.window),
		0,
	)
	if res == 0 {
		return err
	}

	return nil
}

func (t *winTray) delFromVisibleItems(parent, val uint32) {
	t.muVisibleItems.Lock()
	defer t.muVisibleItems.Unlock()
	visibleItems := t.visibleItems[parent]
	for i, itemval := range visibleItems {
		if val == itemval {
			t.visibleItems[parent] = append(visibleItems[:i], visibleItems[i+1:]...)
			break
		}
	}
}

func (t *winTray) addToVisibleItems(parent, val uint32) {
	t.muVisibleItems.Lock()
	defer t.muVisibleItems.Unlock()
	if visibleItems, exists := t.visibleItems[parent]; !exists {
		t.visibleItems[parent] = []uint32{val}
	} else {
		newvisible := append(visibleItems, val)
		sort.Slice(newvisible, func(i, j int) bool { return newvisible[i] < newvisible[j] })
		t.visibleItems[parent] = newvisible
	}
}

func (t *winTray) getVisibleItemIndex(parent, val uint32) int {
	t.muVisibleItems.RLock()
	defer t.muVisibleItems.RUnlock()
	for i, itemval := range t.visibleItems[parent] {
		if val == itemval {
			return i
		}
	}
	return -1
}

// iconFromBytes returns the HICON for the given .ico bytes, building each
// distinct icon once. Nothing touches the file system: CreateIconFromResourceEx
// makes the icon from the bits the .ico file already holds.
func (t *winTray) iconFromBytes(iconBytes []byte) (windows.Handle, error) {
	key := iconCacheKey(iconBytes)

	t.muLoadedImages.RLock()
	h, ok := t.loadedImages[key]
	t.muLoadedImages.RUnlock()
	if ok {
		return h, nil
	}

	bits, w, h2, err := selectIconBits(iconBytes, iconSize())
	if err != nil {
		return 0, err
	}

	res, _, callErr := pCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&bits[0])),
		uintptr(len(bits)),
		1,          // fIcon: the bits describe an icon, not a cursor
		0x00030000, // dwVer, the version every current .ico uses
		uintptr(w),
		uintptr(h2),
		0, // LR_DEFAULTCOLOR
	)
	if res == 0 {
		return 0, callError(callErr, "CreateIconFromResourceEx failed")
	}

	h = windows.Handle(res)
	t.muLoadedImages.Lock()
	t.loadedImages[key] = h
	t.muLoadedImages.Unlock()
	return h, nil
}

// iconCacheKey is the digest the loaded-icon cache is keyed on, so that the same
// icon handed over twice is built only once.
func iconCacheKey(iconBytes []byte) string {
	sum := md5.Sum(iconBytes)
	return hex.EncodeToString(sum[:])
}

// selectIconBits parses an .ico file and returns the bits of the entry that fits
// the requested size, along with that entry's dimensions.
//
// The .ico directory has the same layout as an RT_GROUP_ICON resource: a six
// byte header followed by sixteen byte entries, each naming an offset and a
// length. CreateIconFromResourceEx wants a DWORD-aligned buffer and an .ico
// offset is not guaranteed to be aligned, so the chosen bits are copied out.
func selectIconBits(iconBytes []byte, target int) ([]byte, int, int, error) {
	var (
		headerLen = 6
		entryLen  = 16
	)
	if len(iconBytes) < headerLen {
		return nil, 0, 0, fmt.Errorf("icon: %d bytes is too short for an .ico file", len(iconBytes))
	}
	if typ := binary.LittleEndian.Uint16(iconBytes[2:4]); typ != 1 {
		return nil, 0, 0, fmt.Errorf("icon: not an .ico file (type %d)", typ)
	}
	count := int(binary.LittleEndian.Uint16(iconBytes[4:6]))
	if count == 0 || len(iconBytes) < headerLen+count*entryLen {
		return nil, 0, 0, fmt.Errorf("icon: .ico directory claims %d entries, but %d bytes only hold %d",
			count, len(iconBytes), (len(iconBytes)-headerLen)/entryLen)
	}

	type entry struct {
		width, height int
		bytes, offset uint32
	}
	entries := make([]entry, 0, count)
	for i := range count {
		off := headerLen + i*entryLen
		w, h := int(iconBytes[off]), int(iconBytes[off+1])
		// A stored dimension of zero means 256.
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		entries = append(entries, entry{
			width:  w,
			height: h,
			bytes:  binary.LittleEndian.Uint32(iconBytes[off+8 : off+12]),
			offset: binary.LittleEndian.Uint32(iconBytes[off+12 : off+16]),
		})
	}

	// Prefer an exact match; failing that the smallest entry that still covers
	// the target, since downscaling a bigger drawing beats enlarging a smaller
	// one; and failing that the largest entry available.
	better := func(a, b entry) bool {
		exact := func(e entry) bool { return e.width == target && e.height == target }
		if exact(a) != exact(b) {
			return exact(a)
		}
		covers := func(e entry) bool { return e.width >= target && e.height >= target }
		if covers(a) != covers(b) {
			return covers(a)
		}
		if covers(a) {
			return a.width*a.height < b.width*b.height
		}
		return a.width*a.height > b.width*b.height
	}
	best := entries[0]
	for _, e := range entries[1:] {
		if better(e, best) {
			best = e
		}
	}

	end := int(best.offset) + int(best.bytes)
	if best.bytes == 0 || end > len(iconBytes) {
		return nil, 0, 0, fmt.Errorf("icon: .ico entry lies outside the file (offset %d, %d bytes, file %d bytes)",
			best.offset, best.bytes, len(iconBytes))
	}
	bits := make([]byte, best.bytes)
	copy(bits, iconBytes[best.offset:end])
	return bits, best.width, best.height, nil
}

func (t *winTray) iconToBitmap(hIcon windows.Handle) (windows.Handle, error) {
	cx, _, _ := pGetSystemMetrics.Call(SM_CXSMICON)
	cy, _, _ := pGetSystemMetrics.Call(SM_CYSMICON)
	w, h := int(cx), int(cy)

	hDC, _, err := pGetDC.Call(uintptr(0))
	if hDC == 0 {
		return 0, err
	}
	defer pReleaseDC.Call(uintptr(0), hDC)

	// A 32-bit top-down DIB, so that the alpha channel is ours to write. A menu
	// blends a bitmap item against its alpha; a compatible bitmap has none, and
	// the black that transparent pixels are stored as reaches the menu instead.
	bmi := bitmapInfo{header: bitmapInfoHeader{
		size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		width:       int32(w),
		height:      -int32(h), // negative: rows run from the top down
		planes:      1,
		bitCount:    32,
		compression: biRGB,
	}}
	var pixels *byte
	hBmp, _, callErr := pCreateDIBSection.Call(hDC, uintptr(unsafe.Pointer(&bmi)), dibRGBColors, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if hBmp == 0 || pixels == nil {
		return 0, callError(callErr, "CreateDIBSection failed")
	}
	dst := unsafe.Slice(pixels, w*h*4)

	// An icon that carries its own alpha is copied from its colour bitmap:
	// DrawIconEx does not write the alpha channel, so drawing it would throw the
	// transparency away.
	if src, sw, sh, ok := iconColorPixels(hDC, hIcon); ok {
		scaleBGRA(src, sw, sh, dst, w, h)
		premultiplyAlpha(dst)
		return windows.Handle(hBmp), nil
	}

	// Otherwise the AND mask is the only transparency an icon has: draw the
	// colour and derive alpha from the mask.
	hMemDC, _, err := pCreateCompatibleDC.Call(hDC)
	if hMemDC == 0 {
		pDeleteObject.Call(hBmp)
		return 0, callError(err, "CreateCompatibleDC failed")
	}
	defer pDeleteDC.Call(hMemDC)
	hOriginalBmp, _, _ := pSelectObject.Call(hMemDC, hBmp)
	defer pSelectObject.Call(hMemDC, hOriginalBmp)

	if res, _, err := pDrawIconEx.Call(hMemDC, 0, 0, uintptr(hIcon), cx, cy, 0, uintptr(0), DI_NORMAL); res == 0 {
		pDeleteObject.Call(hBmp)
		return 0, callError(err, "DrawIconEx failed")
	}
	applyMaskAlpha(hDC, hIcon, dst, w, h)
	premultiplyAlpha(dst)
	return windows.Handle(hBmp), nil
}

// bitmapInfoHeader and bitmapInfo describe pixels handed to, or asked of, GDI.
type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [3]uint32 // a palette for indexed formats, unused by BI_RGB
}

// bitmap mirrors the Windows BITMAP struct, which GetObject fills in.
type bitmap struct {
	typ        int32
	width      int32
	height     int32
	widthBytes int32
	planes     uint16
	bitsPixel  uint16
	bits       uintptr
}

// iconInfo mirrors the Windows ICONINFO struct. The two bitmaps it hands back
// are copies the caller owns and has to delete.
type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

// iconColorPixels returns an icon's own pixels, top-down, when its colour bitmap
// is 32 bits and therefore carries an alpha channel.
func iconColorPixels(hDC uintptr, hIcon windows.Handle) ([]byte, int, int, bool) {
	var ii iconInfo
	if r, _, _ := pGetIconInfo.Call(uintptr(hIcon), uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil, 0, 0, false
	}
	defer deleteIconInfo(&ii)

	var bm bitmap
	if r, _, _ := pGetObject.Call(uintptr(ii.hbmColor), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil, 0, 0, false
	}
	if bm.bitsPixel != 32 || bm.width <= 0 || bm.height <= 0 {
		return nil, 0, 0, false
	}
	w, h := int(bm.width), int(bm.height)

	bmi := bitmapInfo{header: bitmapInfoHeader{
		size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		width:       int32(w),
		height:      -int32(h),
		planes:      1,
		bitCount:    32,
		compression: biRGB,
	}}
	data := make([]byte, w*h*4)
	if r, _, _ := pGetDIBits.Call(hDC, uintptr(ii.hbmColor), 0, uintptr(h), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors); r == 0 {
		return nil, 0, 0, false
	}
	return data, w, h, true
}

// scaleBGRA copies source pixels into the destination, taking the nearest pixel
// when the sizes differ. The alpha byte travels with the colour, which is the
// point of going through the colour bitmap at all.
func scaleBGRA(src []byte, sw, sh int, dst []byte, dw, dh int) {
	if sw == dw && sh == dh {
		copy(dst, src)
		return
	}
	for y := 0; y < dh; y++ {
		sy := y * sh / dh
		for x := 0; x < dw; x++ {
			sx := x * sw / dw
			so := (sy*sw + sx) * 4
			do := (y*dw + x) * 4
			copy(dst[do:do+4], src[so:so+4])
		}
	}
}

// premultiplyAlpha scales each colour channel by its alpha. An icon stores
// straight alpha, but a menu blends an item bitmap as though it were
// premultiplied: a semi-transparent edge pixel would otherwise contribute its
// full colour and leave a halo around the icon. Nothing else wants premultiplied
// pixels, so this is done here, on the bitmap the menu takes, and not on the
// icon the shell takes.
func premultiplyAlpha(pixels []byte) {
	for i := 0; i+3 < len(pixels); i += 4 {
		a := uint32(pixels[i+3])
		switch {
		case a == 0:
			pixels[i], pixels[i+1], pixels[i+2] = 0, 0, 0
		case a < 255:
			pixels[i] = byte(uint32(pixels[i]) * a / 255)
			pixels[i+1] = byte(uint32(pixels[i+1]) * a / 255)
			pixels[i+2] = byte(uint32(pixels[i+2]) * a / 255)
		}
	}
}

// applyMaskAlpha sets the alpha channel from the icon's AND mask, where a set bit
// means the pixel is transparent.
func applyMaskAlpha(hDC uintptr, hIcon windows.Handle, pixels []byte, w, h int) {
	var ii iconInfo
	if r, _, _ := pGetIconInfo.Call(uintptr(hIcon), uintptr(unsafe.Pointer(&ii))); r == 0 {
		return
	}
	defer deleteIconInfo(&ii)

	// The mask holds one bit per pixel; GDI hands it back one byte per pixel.
	bmi := bitmapInfo{header: bitmapInfoHeader{
		size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		width:       int32(w),
		height:      -int32(h),
		planes:      1,
		bitCount:    8,
		compression: biRGB,
	}}
	mask := make([]byte, w*h)
	if r, _, _ := pGetDIBits.Call(hDC, uintptr(ii.hbmMask), 0, uintptr(h), uintptr(unsafe.Pointer(&mask[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors); r == 0 {
		return
	}
	for i := 0; i < w*h; i++ {
		if mask[i] != 0 {
			pixels[i*4+3] = 0
		} else {
			pixels[i*4+3] = 255
		}
	}
}

// deleteIconInfo releases the bitmaps GetIconInfo created.
func deleteIconInfo(ii *iconInfo) {
	if ii.hbmColor != 0 {
		pDeleteObject.Call(uintptr(ii.hbmColor))
	}
	if ii.hbmMask != 0 {
		pDeleteObject.Call(uintptr(ii.hbmMask))
	}
}

func registerSystray() {
	if err := wt.initInstance(); err != nil {
		logError("unable to init instance", "error", err)
		return
	}

	if err := wt.createMenu(); err != nil {
		logError("unable to create menu", "error", err)
		return
	}

	systrayReady()
}

func nativeLoop() {
	// Main message pump.
	m := &struct {
		WindowHandle windows.Handle
		Message      uint32
		Wparam       uintptr
		Lparam       uintptr
		Time         uint32
		Pt           point
	}{}
	for {
		ret, _, err := pGetMessage.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)

		// If the function retrieves a message other than WM_QUIT, the return value is nonzero.
		// If the function retrieves the WM_QUIT message, the return value is zero.
		// If there is an error, the return value is -1
		// https://msdn.microsoft.com/en-us/library/windows/desktop/ms644936(v=vs.85).aspx
		switch int32(ret) {
		case -1:
			logError("error in message loop", "error", err)
			return
		case 0:
			return
		default:
			pTranslateMessage.Call(uintptr(unsafe.Pointer(m)))
			pDispatchMessage.Call(uintptr(unsafe.Pointer(m)))
		}
	}
}

func quit() {
	pPostMessage.Call(
		uintptr(wt.window),
		WM_CLOSE,
		0,
		0,
	)
}

// SetIcon sets the systray icon.
// iconBytes should be the content of .ico for windows and .ico/.jpg/.png
// for other platforms.
func SetIcon(iconBytes []byte) {
	if err := wt.setIcon(iconBytes); err != nil {
		logError("unable to set icon", "error", err)
	}
}

// SetTemplateIcon sets the systray icon as a template icon (on macOS), falling back
// to a regular icon on other platforms.
// templateIconBytes and iconBytes should be the content of .ico for windows and
// .ico/.jpg/.png for other platforms.
func SetTemplateIcon(templateIconBytes []byte, regularIconBytes []byte) {
	SetIcon(regularIconBytes)
}

// SetTitle sets the systray title, only available on Mac and Linux.
func SetTitle(title string) {
	// do nothing
}

func (item *MenuItem) parentId() uint32 {
	if item.parent != nil {
		return uint32(item.parent.id)
	}
	return 0
}

// SetIcon sets the icon of a menu item. Only works on macOS and Windows.
// iconBytes should be the content of .ico/.jpg/.png
func (item *MenuItem) SetIcon(iconBytes []byte) {
	h, err := wt.iconFromBytes(iconBytes)
	if err != nil {
		logError("unable to load icon", "error", err)
		return
	}

	h, err = wt.iconToBitmap(h)
	if err != nil {
		logError("unable to convert icon to bitmap", "error", err)
		return
	}
	wt.muMenuItemIcons.Lock()
	wt.menuItemIcons[uint32(item.id)] = h
	wt.muMenuItemIcons.Unlock()

	err = wt.addOrUpdateMenuItem(uint32(item.id), item.parentId(), item.title, item.disabled, item.checked)
	if err != nil {
		logError("unable to add or update menu item", "error", err)
		return
	}
}

// SetTooltip sets the systray tooltip to display on mouse hover of the tray icon,
// only available on Mac and Windows.
func SetTooltip(tooltip string) {
	if err := wt.setTooltip(tooltip); err != nil {
		logError("unable to set tooltip", "error", err)
		return
	}
}

// SetRemovalAllowed sets whether a user can remove the systray icon or not.
// This is only supported on macOS.
func SetRemovalAllowed(allowed bool) {
}

// SetOnLeftClick sets a callback to be invoked when the tray icon is left-clicked.
// This is only supported on Windows; on macOS and Linux it does nothing.
// If no callback is set, left-clicking falls back to showing the menu (the same
// behaviour as right-clicking). It can be called from any goroutine.
func SetOnLeftClick(callback func()) {
	muOnLeftClick.Lock()
	onLeftClick = callback
	muOnLeftClick.Unlock()
}

func addOrUpdateMenuItem(item *MenuItem) {
	err := wt.addOrUpdateMenuItem(uint32(item.id), item.parentId(), item.title, item.disabled, item.checked)
	if err != nil {
		logError("unable to add or update menu item", "error", err)
		return
	}
}

// SetTemplateIcon sets the icon of a menu item as a template icon (on macOS). On Windows, it
// falls back to the regular icon bytes and on Linux it does nothing.
// templateIconBytes and regularIconBytes should be the content of .ico for windows and
// .ico/.jpg/.png for other platforms.
func (item *MenuItem) SetTemplateIcon(templateIconBytes []byte, regularIconBytes []byte) {
	item.SetIcon(regularIconBytes)
}

func addSeparator(id uint32) {
	err := wt.addSeparatorMenuItem(id, 0)
	if err != nil {
		logError("unable to add separator", "error", err)
		return
	}
}

func hideMenuItem(item *MenuItem) {
	err := wt.hideMenuItem(uint32(item.id), item.parentId())
	if err != nil {
		logError("unable to hide menu item", "error", err)
		return
	}
}

func showMenuItem(item *MenuItem) {
	addOrUpdateMenuItem(item)
}

// iconSize is the edge, in pixels, at which a vector icon is rasterized.
func iconSize() int {
	if cx, _, _ := pGetSystemMetrics.Call(SM_CXSMICON); cx > 0 {
		return int(cx)
	}
	return 32
}

// isNativeIconFormat reports whether data of the given format can be handed to the
// Windows back-end as it is. Only .ico can: LoadImage reads no other image format
// off disk.
//
// Handing an .ico over untouched has a second benefit: decoding and re-encoding
// one would flatten it to a single entry, discarding any extra sizes the caller
// packed into it.
func isNativeIconFormat(format string) bool {
	return format == "ico"
}

// encodeIcon encodes img as the .ico the Windows back-end loads.
func encodeIcon(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := ico.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("convert to .ico: %w", err)
	}
	return buf.Bytes(), nil
}
