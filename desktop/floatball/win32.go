//go:build windows

package floatball

import (
	"math"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdiplus  = syscall.NewLazyDLL("gdiplus.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessageW    = user32.NewProc("DispatchMessageW")
	pIsDialogMessageW    = user32.NewProc("IsDialogMessageW")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pSendMessageW        = user32.NewProc("SendMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pLoadCursorW         = user32.NewProc("LoadCursorW")
	pLoadIconW           = user32.NewProc("LoadIconW")
	pSetTimer            = user32.NewProc("SetTimer")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pGetWindowRect       = user32.NewProc("GetWindowRect")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pSetCapture          = user32.NewProc("SetCapture")
	pReleaseCapture      = user32.NewProc("ReleaseCapture")
	pUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	pGetDC               = user32.NewProc("GetDC")
	pReleaseDC           = user32.NewProc("ReleaseDC")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pSetFocus            = user32.NewProc("SetFocus")
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pMessageBoxW         = user32.NewProc("MessageBoxW")
	pMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	pSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	pOpenClipboard       = user32.NewProc("OpenClipboard")
	pEmptyClipboard      = user32.NewProc("EmptyClipboard")
	pSetClipboardData    = user32.NewProc("SetClipboardData")
	pCloseClipboard      = user32.NewProc("CloseClipboard")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pTrackMouseEvent     = user32.NewProc("TrackMouseEvent")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")
	pCreateFontW        = gdi32.NewProc("CreateFontW")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")
	pGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")

	pRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")

	pGdiplusStartup                    = gdiplus.NewProc("GdiplusStartup")
	pGdipCreateBitmapFromScan0         = gdiplus.NewProc("GdipCreateBitmapFromScan0")
	pGdipGetImageGraphicsContext       = gdiplus.NewProc("GdipGetImageGraphicsContext")
	pGdipDeleteGraphics                = gdiplus.NewProc("GdipDeleteGraphics")
	pGdipDisposeImage                  = gdiplus.NewProc("GdipDisposeImage")
	pGdipSetSmoothingMode              = gdiplus.NewProc("GdipSetSmoothingMode")
	pGdipSetTextRenderingHint          = gdiplus.NewProc("GdipSetTextRenderingHint")
	pGdipGraphicsClear                 = gdiplus.NewProc("GdipGraphicsClear")
	pGdipCreateSolidFill               = gdiplus.NewProc("GdipCreateSolidFill")
	pGdipDeleteBrush                   = gdiplus.NewProc("GdipDeleteBrush")
	pGdipFillEllipse                   = gdiplus.NewProc("GdipFillEllipse")
	pGdipCreatePen1                    = gdiplus.NewProc("GdipCreatePen1")
	pGdipDeletePen                     = gdiplus.NewProc("GdipDeletePen")
	pGdipDrawEllipse                   = gdiplus.NewProc("GdipDrawEllipse")
	pGdipCreatePath                    = gdiplus.NewProc("GdipCreatePath")
	pGdipAddPathArc                    = gdiplus.NewProc("GdipAddPathArc")
	pGdipClosePathFigure               = gdiplus.NewProc("GdipClosePathFigure")
	pGdipFillPath                      = gdiplus.NewProc("GdipFillPath")
	pGdipDrawPath                      = gdiplus.NewProc("GdipDrawPath")
	pGdipDeletePath                    = gdiplus.NewProc("GdipDeletePath")
	pGdipCreateFontFamilyFromName      = gdiplus.NewProc("GdipCreateFontFamilyFromName")
	pGdipGetGenericFontFamilySansSerif = gdiplus.NewProc("GdipGetGenericFontFamilySansSerif")
	pGdipCreateFont                    = gdiplus.NewProc("GdipCreateFont")
	pGdipDeleteFont                    = gdiplus.NewProc("GdipDeleteFont")
	pGdipCreateStringFormat            = gdiplus.NewProc("GdipCreateStringFormat")
	pGdipDeleteStringFormat            = gdiplus.NewProc("GdipDeleteStringFormat")
	pGdipSetStringFormatAlign          = gdiplus.NewProc("GdipSetStringFormatAlign")
	pGdipSetStringFormatLineAlign      = gdiplus.NewProc("GdipSetStringFormatLineAlign")
	pGdipSetStringFormatTrimming       = gdiplus.NewProc("GdipSetStringFormatTrimming")
	pGdipDrawString                    = gdiplus.NewProc("GdipDrawString")
)

const (
	WS_POPUP            = 0x80000000
	WS_CHILD            = 0x40000000
	WS_VISIBLE          = 0x10000000
	WS_CAPTION          = 0x00C00000
	WS_SYSMENU          = 0x00080000
	WS_TABSTOP          = 0x00010000
	WS_EX_TOPMOST       = 0x00000008
	WS_EX_TOOLWINDOW    = 0x00000080
	WS_EX_LAYERED       = 0x00080000
	WS_EX_CLIENTEDGE    = 0x00000200
	WS_EX_DLGMODALFRAME = 0x00000001

	WM_DESTROY       = 0x0002
	WM_CLOSE         = 0x0010
	WM_SETFONT       = 0x0030
	WM_MOUSEACTIVATE = 0x0021
	WM_DISPLAYCHANGE = 0x007E
	WM_COMMAND       = 0x0111
	WM_TIMER         = 0x0113
	WM_MOUSEMOVE     = 0x0200
	WM_LBUTTONDOWN   = 0x0201
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_MOUSELEAVE    = 0x02A3
	WM_APP           = 0x8000
	EM_SETSEL        = 0x00B1

	CS_DBLCLKS    = 0x0008
	MA_NOACTIVATE = 3

	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010

	MF_STRING    = 0x0000
	MF_GRAYED    = 0x0001
	MF_CHECKED   = 0x0008
	MF_POPUP     = 0x0010
	MF_SEPARATOR = 0x0800

	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	ULW_ALPHA    = 2
	AC_SRC_ALPHA = 1
)

var HWND_TOPMOST = ^uintptr(0) // (HWND)-1

type POINT struct{ X, Y int32 }
type SIZE struct{ CX, CY int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MSG struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       POINT
	LPrivate uint32
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BITMAPINFO struct {
	Header BITMAPINFOHEADER
	Colors [1]uint32
}

type BLENDFUNCTION struct {
	BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat byte
}

type MONITORINFO struct {
	CbSize  uint32
	Monitor RECT
	Work    RECT
	Flags   uint32
}

type TRACKMOUSEEVENT struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

type RectF struct{ X, Y, W, H float32 }

type gdiplusStartupInput struct {
	Version                  uint32
	DebugEventCallback       uintptr
	SuppressBackgroundThread int32
	SuppressExternalCodecs   int32
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}
func u16p(s string) uintptr { return uintptr(unsafe.Pointer(u16(s))) }

// f passes a float32 to a Win32/GDI+ function. Go's stdcall trampoline on
// amd64 mirrors the first four args into XMM0-3, and stack args are read as
// raw 32-bit values, so the raw bits are correct in every position.
func f(v float32) uintptr { return uintptr(math.Float32bits(v)) }

func call(p *syscall.LazyProc, a ...uintptr) uintptr {
	r, _, _ := p.Call(a...)
	return r
}

func loword(v uintptr) int { return int(int16(v & 0xffff)) }
