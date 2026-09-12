package systray

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	ico "github.com/biessek/golang-ico"
	"golang.org/x/sys/windows"
)

// testIcon generates a small solid-color .ico in memory, so the tests don't
// depend on the example application's embedded icon.
func testIcon(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := ico.Encode(&buf, img); err != nil {
		t.Fatalf("encode test icon: %v", err)
	}
	return buf.Bytes()
}

func TestBaseWindowsTray(t *testing.T) {
	systrayReady = func() {}
	systrayExit = func() {}

	runtime.LockOSThread()

	if err := wt.initInstance(); err != nil {
		t.Fatalf("initInstance failed: %s", err)
	}

	if err := wt.createMenu(); err != nil {
		t.Fatalf("createMenu failed: %s", err)
	}

	defer func() {
		pDestroyWindow.Call(uintptr(wt.window))
		wt.wcex.unregister()
	}()

	iconPath := filepath.Join(t.TempDir(), "tray.ico")
	if err := os.WriteFile(iconPath, testIcon(t), 0644); err != nil {
		t.Fatalf("write temp icon: %v", err)
	}
	if err := wt.setIcon(iconPath); err != nil {
		t.Errorf("SetIcon failed: %s", err)
	}

	if err := wt.setTooltip("Cyrillic tooltip тест:)"); err != nil {
		t.Errorf("SetIcon failed: %s", err)
	}

	var id uint32 = 0
	err := wt.addOrUpdateMenuItem(atomic.AddUint32(&id, 1), 0, "Simple enabled", false, false)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}
	err = wt.addOrUpdateMenuItem(atomic.AddUint32(&id, 1), 0, "Simple disabled", true, false)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}
	err = wt.addSeparatorMenuItem(atomic.AddUint32(&id, 1), 0)
	if err != nil {
		t.Errorf("addSeparatorMenuItem failed: %s", err)
	}
	err = wt.addOrUpdateMenuItem(atomic.AddUint32(&id, 1), 0, "Simple checked enabled", false, true)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}
	err = wt.addOrUpdateMenuItem(atomic.AddUint32(&id, 1), 0, "Simple checked disabled", true, true)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}

	err = wt.hideMenuItem(1, 0)
	if err != nil {
		t.Errorf("hideMenuItem failed: %s", err)
	}

	err = wt.hideMenuItem(100, 0)
	if err == nil {
		t.Error("hideMenuItem failed: must return error on invalid item id")
	}

	err = wt.addOrUpdateMenuItem(2, 0, "Simple disabled update", true, false)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}

	time.AfterFunc(1*time.Second, quit)

	m := struct {
		WindowHandle windows.Handle
		Message      uint32
		Wparam       uintptr
		Lparam       uintptr
		Time         uint32
		Pt           point
	}{}
	for {
		ret, _, err := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		res := int32(ret)
		if res == -1 {
			t.Errorf("win32 GetMessage failed: %v", err)
			return
		} else if res == 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func TestWindowsRun(t *testing.T) {
	iconBytes := testIcon(t)
	onReady := func() {
		SetIcon(iconBytes)
		SetTitle("Test title с кириллицей")

		bSomeBtn := AddMenuItem("Йа кнопко", "")
		bSomeBtn.Check()
		AddSeparator()
		bQuit := AddMenuItem("Quit", "Quit the whole app")
		go func() {
			<-bQuit.ClickedCh
			t.Log("Quit reqested")
			Quit()
		}()
		time.AfterFunc(1*time.Second, Quit)
	}

	onExit := func() {
		t.Log("Exit success")
	}

	Run(onReady, onExit)
}
