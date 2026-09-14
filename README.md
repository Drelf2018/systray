# systray

本仓库是 [getlantern/systray](https://github.com/getlantern/systray) 的延续分支（上游自 v1.2.0 之后基本停更）。
模块路径为 `github.com/Drelf2018/systray`，最低 Go 版本 1.27。

## 相对上游的改动

### 图标

- 新增 **`ToICON(data []byte) ([]byte, error)`**：把任意图片字节转成当前平台能直接加载的图标格式，结果可以直接交给 `SetIcon`。
  已经是后端原生格式的（Windows 的 `.ico`，其余平台的 `.png`/`.jpeg`/`.ico`）原样透传——不解码、不重新编码、不缩放；其余格式（PNG、JPEG、GIF、BMP、TIFF、WebP、**SVG**）解码后重新编码。认不出来的数据报错返回，不会交出一个残缺图标。
- SVG 由 [github.com/Drelf2018/exp/svg](https://github.com/Drelf2018/exp/svg) 处理，并且**直接按平台要求的尺寸光栅化**（Windows 上即 `SM_CXSMICON`），不经过中间位图，也不做二次缩放。
- Windows 不再落地临时文件：`.ico` 从内存中的资源位图直接构建（`CreateIconFromResourceEx`），并按 `SM_CXSMICON` 挑选最合适的一档。
- 菜单项图标带真正的 alpha 通道（32 位自顶向下 DIB、预乘 alpha），不会再有黑底或悬停时才消失的彩边；替换图标时会释放上一张位图。

### API

- 新增 **`EnableDPIAwareness() error`**（仅 Windows，其余平台为空实现）：由宿主决定是否声明 DPI 感知。
  systray 自己不会调用它——声明感知会让 Windows 停止拉伸本进程的窗口、改发物理像素，手写像素布局的界面可能因此错位。它必须在第一个窗口出现之前调用；Windows 若发现进程已有自己的模式（清单、UI 框架或更早的一次调用）会拒绝，这种拒绝按成功上报，因为进程无论如何已经是感知的。
- 新增 **`Logger *slog.Logger`**：日志改用标准库 `log/slog`，不再依赖 `golog`。不设置时写 `slog.Default()`。
- 新增 **`SetOnLeftClick(callback func())`**（仅 Windows）：区分左右键点击；未设置回调时，左键点击回退为弹出菜单。
- `SetIcon` 收到空字节切片不再 panic：记一条日志，并保持当前的图标不变。

### 其他

- 模块路径改为 `github.com/Drelf2018/systray`，最低 Go 版本 1.27。
- Windows 后端是纯 Go，可以 `CGO_ENABLED=0` 构建；macOS 与 Linux 仍然需要 cgo。
- 移除仓库内的示例程序；日志去掉了 `golog` 依赖；另有若干小修（`hideMenuItem` 吞掉错误、Linux 菜单项结构体泄漏等）。

## 从上游迁移

把 import 路径从 `github.com/getlantern/systray` 换成 `github.com/Drelf2018/systray` 即可，初始化与菜单用法不变。
新增的 `ToICON`、`EnableDPIAwareness`、`Logger` 都是可选项，不调用也照常工作。

## 用法

```go
package main

import (
	_ "embed"
	"log"

	"github.com/Drelf2018/systray"
)

//go:embed icon.svg
var iconData []byte

func main() {
	// 仅 Windows，且必须在任何窗口出现之前调用。
	if err := systray.EnableDPIAwareness(); err != nil {
		log.Println(err)
	}

	// 任意图片格式进，平台要的图标格式出。
	icon, err := systray.ToICON(iconData)
	if err != nil {
		log.Fatal(err)
	}

	systray.Run(func() {
		systray.SetIcon(icon)
		systray.SetTitle("Awesome App")
		systray.SetTooltip("Pretty awesome 超级棒")

		mQuit := systray.AddMenuItem("Quit", "Quit the whole app")
		go func() {
			<-mQuit.ClickedCh
			systray.Quit()
		}()
	}, func() {
		// 退出前清理
	})
}
```

菜单项可以勾选、禁用、隐藏，也可以带图标：

```go
mCheck := systray.AddMenuItemCheckbox("Enabled", "Toggle it", true)
mCheck.Check()
mCheck.Disable()
mCheck.SetIcon(icon) // 仅 Windows 与 macOS
mCheck.Hide()
```

---

systray is a cross-platform Go library to place an icon and menu in the notification area.

## Features

* Supported on Windows, macOS, and Linux
* Menu items can be checked and/or disabled
* Icons may be given as PNG, JPEG, GIF, BMP, TIFF, WebP, ICO or SVG bytes, converted with `ToICON`
* Most functions may be called from any goroutine

## API

See the [full API reference](https://pkg.go.dev/github.com/Drelf2018/systray) as well as the [CHANGELOG](https://github.com/Drelf2018/systray/tree/master/CHANGELOG.md).

The package needs cgo on macOS and Linux, so make sure `CGO_ENABLED=1` there. On Windows the back-end is pure Go and builds with `CGO_ENABLED=0`.

## Platform notes

### Linux

* Building apps requires gcc as well as the `gtk3` and `libayatana-appindicator3` development headers to be installed. For Debian or Ubuntu, you may install these using:

```sh
sudo apt-get install gcc libgtk-3-dev libayatana-appindicator3-dev
```

On Linux Mint, `libxapp-dev` is also required.

If you need to support the older `libappindicator3` library instead, you can pass the build flag `legacy_appindicator`
when building. For example:

```
go build -tags=legacy_appindicator
```

### Windows

* To avoid opening a console at application startup, use these compile flags:

```sh
go build -ldflags -H=windowsgui
```

* Call `systray.EnableDPIAwareness()` before creating any window if you want the tray icon rasterized at the size the
  display actually draws it at, and if your interface is ready to lay itself out in physical pixels.

### macOS

On macOS, you will need to create an application bundle to wrap the binary; simply folders with the following minimal structure and assets:

```
SystrayApp.app/
  Contents/
    Info.plist
    MacOS/
      go-executable
    Resources/
      SystrayApp.icns
```

When running as an app bundle, you may want to add one or both of the following to your Info.plist:

```xml
<!-- avoid having a blurry icon and text -->
	<key>NSHighResolutionCapable</key>
	<string>True</string>

	<!-- avoid showing the app on the Dock -->
	<key>LSUIElement</key>
	<string>1</string>
```

Consult the [Official Apple Documentation here](https://developer.apple.com/library/archive/documentation/CoreFoundation/Conceptual/CFBundles/BundleTypes/BundleTypes.html#//apple_ref/doc/uid/10000123i-CH101-SW1).

On macOS, it's possible to set the underlying
[`NSStatusItemBehavior`](https://developer.apple.com/documentation/appkit/nsstatusitembehavior?language=objc)
with `systray.SetRemovalAllowed(true)`. When enabled, the user can cmd-drag the
icon off the menu bar.

## Credits

- https://github.com/xilp/systray
- https://github.com/cratonica/trayhost
