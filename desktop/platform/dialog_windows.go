package platform

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The modern Explorer picker (IFileOpenDialog), called through its COM
// vtable so the app needs no cgo.

var procCoCreateInstance = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")

var (
	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

const (
	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	fosAllowMultiSelect = 0x200
	fosPathMustExist    = 0x800
	fosFileMustExist    = 0x1000
	sigdnFileSysPath    = 0x80058000
	hrCancelled         = 0x800704C7
	clsctxInprocServer  = 1
)

// Vtable slots: IUnknown, IModalWindow, IFileDialog, IFileOpenDialog,
// IShellItem and IShellItemArray.
const (
	vRelease        = 2
	vShow           = 3
	vSetFileTypes   = 4
	vSetOptions     = 9
	vGetOptions     = 10
	vSetTitle       = 17
	vGetResults     = 27
	vItemGetDisplay = 5
	vArrayGetCount  = 7
	vArrayGetItemAt = 8
)

type filterSpec struct{ name, spec *uint16 }

func call(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, slot*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func pick(owner uintptr, title string, opts uint32, filters []Filter) ([]string, error) {
	type result struct {
		paths []string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		// A fresh thread that never unlocks: it ends with the goroutine,
		// taking its COM apartment with it.
		runtime.LockOSThread()
		paths, err := pickOnThread(owner, title, opts, filters)
		ch <- result{paths, err}
	}()
	r := <-ch
	return r.paths, r.err
}

func pickOnThread(owner uintptr, title string, opts uint32, filters []Filter) ([]string, error) {
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
		defer windows.CoUninitialize()
	}
	dlg := new(unsafe.Pointer)
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(dlg))); hr != 0 {
		return nil, fmt.Errorf("the file dialog is not available (0x%x)", hr)
	}
	d := *dlg
	defer call(d, vRelease)

	cur := new(uint32)
	call(d, vGetOptions, uintptr(unsafe.Pointer(cur)))
	call(d, vSetOptions, uintptr(*cur|opts|fosForceFileSystem|fosPathMustExist))
	if title != "" {
		t, _ := windows.UTF16PtrFromString(title)
		call(d, vSetTitle, uintptr(unsafe.Pointer(t)))
	}
	if len(filters) > 0 {
		specs := make([]filterSpec, len(filters))
		for i, f := range filters {
			specs[i].name, _ = windows.UTF16PtrFromString(f.Name)
			specs[i].spec, _ = windows.UTF16PtrFromString(f.Pattern)
		}
		call(d, vSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0])))
	}
	switch hr := uint32(call(d, vShow, owner)); hr {
	case 0:
	case hrCancelled:
		return nil, ErrCancelled
	default:
		return nil, fmt.Errorf("the file dialog failed (0x%x)", hr)
	}

	arr := new(unsafe.Pointer)
	if hr := call(d, vGetResults, uintptr(unsafe.Pointer(arr))); hr != 0 {
		return nil, fmt.Errorf("the file dialog returned nothing (0x%x)", hr)
	}
	defer call(*arr, vRelease)
	n := new(uint32)
	call(*arr, vArrayGetCount, uintptr(unsafe.Pointer(n)))
	var paths []string
	for i := range *n {
		item := new(unsafe.Pointer)
		if call(*arr, vArrayGetItemAt, uintptr(i), uintptr(unsafe.Pointer(item))) != 0 {
			continue
		}
		name := new(*uint16)
		if call(*item, vItemGetDisplay, sigdnFileSysPath, uintptr(unsafe.Pointer(name))) == 0 {
			paths = append(paths, windows.UTF16PtrToString(*name))
			windows.CoTaskMemFree(unsafe.Pointer(*name))
		}
		call(*item, vRelease)
	}
	if len(paths) == 0 {
		return nil, ErrCancelled
	}
	return paths, nil
}

// PickFolder shows the system folder picker over owner (a window handle,
// or 0).
func PickFolder(owner uintptr, title string) (string, error) {
	p, err := pick(owner, title, fosPickFolders, nil)
	if err != nil {
		return "", err
	}
	return p[0], nil
}

// PickFiles shows the system file picker for one or more files.
func PickFiles(owner uintptr, title string, filters ...Filter) ([]string, error) {
	return pick(owner, title, fosAllowMultiSelect|fosFileMustExist, filters)
}
