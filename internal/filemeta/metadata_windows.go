package filemeta

import (
	"golang.org/x/sys/windows"
	"runtime"
	"syscall"
	"unsafe"
)

var ole = windows.NewLazySystemDLL("ole32.dll")
var shell = windows.NewLazySystemDLL("shell32.dll")
var props = windows.NewLazySystemDLL("propsys.dll")

type propertyKey struct {
	Format windows.GUID
	ID     uint32
}
type propertyVariant struct {
	Type     uint16
	Reserved [3]uint16
	Data     [2]uintptr
}
type propertyStore struct{ Table *[8]uintptr }

func failed(hr uintptr) bool { return int32(hr) < 0 }
func call(method uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(method, args...)
	return r
}

// A writable handler is optional and file-type dependent. This never registers
// handlers or claims that an NTFS stream is an Explorer tag.
func Write(path, title, description string, tags []string) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if failed(hr) {
		return "property_store_unavailable"
	}
	defer ole.NewProc("CoUninitialize").Call()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "encoding_failed"
	}
	iid := windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	var store *propertyStore
	hr, _, _ = shell.NewProc("SHGetPropertyStoreFromParsingName").Call(uintptr(unsafe.Pointer(p)), 0, 2, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&store)))
	if failed(hr) || store == nil {
		return "unsupported_property_handler"
	}
	defer call(store.Table[2], uintptr(unsafe.Pointer(store)))
	type entry struct {
		key    propertyKey
		values []string
		vector bool
	}
	items := []entry{{values: []string{title}}, {values: []string{description}}, {values: tags, vector: true}}
	for i, name := range []string{"System.Title", "System.Comment", "System.Keywords"} {
		keyName, _ := windows.UTF16PtrFromString(name)
		hr, _, _ = props.NewProc("PSGetPropertyKeyFromName").Call(uintptr(unsafe.Pointer(keyName)), uintptr(unsafe.Pointer(&items[i].key)))
		if failed(hr) {
			return "property_lookup_failed"
		}
		ptrs := []*uint16{}
		for _, text := range items[i].values {
			ptr, e := windows.UTF16PtrFromString(text)
			if e != nil {
				return "encoding_failed"
			}
			ptrs = append(ptrs, ptr)
		}
		value := propertyVariant{Type: 31}
		if items[i].vector {
			value.Type = 0x101f
			value.Data[0] = uintptr(len(ptrs))
			if len(ptrs) > 0 {
				value.Data[1] = uintptr(unsafe.Pointer(&ptrs[0]))
			}
		} else {
			value.Data[0] = uintptr(unsafe.Pointer(ptrs[0]))
		}
		result := call(store.Table[6], uintptr(unsafe.Pointer(store)), uintptr(unsafe.Pointer(&items[i].key)), uintptr(unsafe.Pointer(&value)))
		runtime.KeepAlive(ptrs)
		if failed(result) {
			return "unsupported_or_write_failed"
		}
	}
	if failed(call(store.Table[7], uintptr(unsafe.Pointer(store)))) {
		return "commit_failed"
	}
	for _, item := range items {
		var value propertyVariant
		if failed(call(store.Table[5], uintptr(unsafe.Pointer(store)), uintptr(unsafe.Pointer(&item.key)), uintptr(unsafe.Pointer(&value)))) {
			return "readback_failed"
		}
		equal := false
		if !item.vector && value.Type == 31 {
			// PROPVARIANT's union contains native pointers. Read the pointer
			// directly from the union storage instead of reconstructing it
			// from a uintptr, which Go's pointer checker cannot validate.
			ptr := *(**uint16)(unsafe.Pointer(&value.Data[0]))
			equal = windows.UTF16PtrToString(ptr) == item.values[0]
		}
		if item.vector && value.Type == 0x101f && value.Data[0] == uintptr(len(item.values)) {
			equal = true
			ptrs := unsafe.Slice(*(***uint16)(unsafe.Pointer(&value.Data[1])), len(item.values))
			for i, ptr := range ptrs {
				if windows.UTF16PtrToString(ptr) != item.values[i] {
					equal = false
				}
			}
		}
		ole.NewProc("PropVariantClear").Call(uintptr(unsafe.Pointer(&value)))
		if !equal {
			return "readback_failed"
		}
	}
	return "applied"
}
