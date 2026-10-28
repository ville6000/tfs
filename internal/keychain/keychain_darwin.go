//go:build darwin

package keychain

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The Security framework is called through purego so that release builds keep CGO_ENABLED=0
// and can still be cross-compiled.

const (
	kCFStringEncodingUTF8 = 0x08000100

	errSecSuccess               = 0
	errSecUserCanceled          = -128
	errSecAuthFailed            = -25293
	errSecItemNotFound          = -25300
	errSecInteractionNotAllowed = -25308
)

var (
	loadOnce sync.Once
	loadErr  error

	cfRelease               func(uintptr)
	cfStringCreateWithBytes func(alloc uintptr, b *byte, n int, enc uint32, external bool) uintptr
	cfDataCreate            func(alloc uintptr, b *byte, n int) uintptr
	cfDataGetLength         func(uintptr) int
	cfDataGetBytePtr        func(uintptr) unsafe.Pointer
	cfDictionaryCreate      func(alloc uintptr, keys, values *uintptr, n int, keyCB, valueCB uintptr) uintptr
	secItemAdd              func(attrs uintptr, result *uintptr) int32
	secItemCopyMatching     func(query uintptr, result *uintptr) int32
	secItemDelete           func(query uintptr) int32

	keyCallBacks, valueCallBacks  uintptr
	cfTrue                        uintptr
	kClass, kClassGenericPassword uintptr
	kService, kAccount, kLabel    uintptr
	kValueData, kReturnData       uintptr
	kMatchLimit, kMatchLimitOne   uintptr
)

func load() error {
	loadOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("keychain: %w", err)
			return
		}
		sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("keychain: %w", err)
			return
		}
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfStringCreateWithBytes, cf, "CFStringCreateWithBytes")
		purego.RegisterLibFunc(&cfDataCreate, cf, "CFDataCreate")
		purego.RegisterLibFunc(&cfDataGetLength, cf, "CFDataGetLength")
		purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
		purego.RegisterLibFunc(&cfDictionaryCreate, cf, "CFDictionaryCreate")
		purego.RegisterLibFunc(&secItemAdd, sec, "SecItemAdd")
		purego.RegisterLibFunc(&secItemCopyMatching, sec, "SecItemCopyMatching")
		purego.RegisterLibFunc(&secItemDelete, sec, "SecItemDelete")

		// The callback structs are passed by address; the other symbols are CFTypeRef globals.
		for _, s := range []struct {
			lib   uintptr
			name  string
			into  *uintptr
			deref bool
		}{
			{cf, "kCFTypeDictionaryKeyCallBacks", &keyCallBacks, false},
			{cf, "kCFTypeDictionaryValueCallBacks", &valueCallBacks, false},
			{cf, "kCFBooleanTrue", &cfTrue, true},
			{sec, "kSecClass", &kClass, true},
			{sec, "kSecClassGenericPassword", &kClassGenericPassword, true},
			{sec, "kSecAttrService", &kService, true},
			{sec, "kSecAttrAccount", &kAccount, true},
			{sec, "kSecAttrLabel", &kLabel, true},
			{sec, "kSecValueData", &kValueData, true},
			{sec, "kSecReturnData", &kReturnData, true},
			{sec, "kSecMatchLimit", &kMatchLimit, true},
			{sec, "kSecMatchLimitOne", &kMatchLimitOne, true},
		} {
			addr, err := purego.Dlsym(s.lib, s.name)
			if err != nil {
				loadErr = fmt.Errorf("keychain: %w", err)
				return
			}
			if s.deref {
				addr = **(**uintptr)(unsafe.Pointer(&addr))
			}
			*s.into = addr
		}
	})
	return loadErr
}

// cfObjects collects CoreFoundation objects to release together.
type cfObjects []uintptr

func (o *cfObjects) add(ref uintptr) uintptr { *o = append(*o, ref); return ref }

func (o *cfObjects) release() {
	for _, r := range *o {
		if r != 0 {
			cfRelease(r)
		}
	}
}

func (o *cfObjects) str(s string) uintptr {
	b := []byte(s)
	ref := cfStringCreateWithBytes(0, unsafe.SliceData(b), len(b), kCFStringEncodingUTF8, false)
	runtime.KeepAlive(b)
	return o.add(ref)
}

func (o *cfObjects) data(s string) uintptr {
	b := []byte(s)
	ref := cfDataCreate(0, unsafe.SliceData(b), len(b))
	runtime.KeepAlive(b)
	clear(b)
	return o.add(ref)
}

func (o *cfObjects) dict(pairs ...uintptr) uintptr {
	var keys, values []uintptr
	for i := 0; i < len(pairs); i += 2 {
		keys = append(keys, pairs[i])
		values = append(values, pairs[i+1])
	}
	ref := cfDictionaryCreate(0, unsafe.SliceData(keys), unsafe.SliceData(values), len(keys), keyCallBacks, valueCallBacks)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(values)
	return o.add(ref)
}

func (o *cfObjects) itemQuery(origin string, extra ...uintptr) uintptr {
	pairs := []uintptr{kClass, kClassGenericPassword, kService, o.str(Service), kAccount, o.str(origin)}
	return o.dict(append(pairs, extra...)...)
}

func statusError(op string, status int32) error {
	switch status {
	case errSecItemNotFound:
		return ErrNotFound
	case errSecUserCanceled, errSecAuthFailed:
		return fmt.Errorf("keychain %s: access was denied", op)
	case errSecInteractionNotAllowed:
		return fmt.Errorf("keychain %s: the Keychain is locked and cannot prompt", op)
	}
	return fmt.Errorf("keychain %s failed (OSStatus %d)", op, status)
}

func get(origin string) (string, error) {
	if err := load(); err != nil {
		return "", err
	}
	var o cfObjects
	defer o.release()
	var result uintptr
	status := secItemCopyMatching(o.itemQuery(origin, kReturnData, cfTrue, kMatchLimit, kMatchLimitOne), &result)
	if status != errSecSuccess {
		return "", statusError("read", status)
	}
	o.add(result)
	n := cfDataGetLength(result)
	if n == 0 {
		return "", ErrNotFound
	}
	return string(unsafe.Slice((*byte)(cfDataGetBytePtr(result)), n)), nil
}

func exists(origin string) (bool, error) {
	if err := load(); err != nil {
		return false, err
	}
	var o cfObjects
	defer o.release()
	switch status := secItemCopyMatching(o.itemQuery(origin, kMatchLimit, kMatchLimitOne), nil); status {
	case errSecSuccess:
		return true, nil
	case errSecItemNotFound:
		return false, nil
	default:
		return false, statusError("lookup", status)
	}
}

func add(origin, token string) error {
	if err := load(); err != nil {
		return err
	}
	var o cfObjects
	defer o.release()
	attrs := o.itemQuery(origin, kLabel, o.str("tfs: "+origin), kValueData, o.data(token))
	if status := secItemAdd(attrs, nil); status != errSecSuccess {
		return statusError("write", status)
	}
	return nil
}

func remove(origin string) error {
	if err := load(); err != nil {
		return err
	}
	var o cfObjects
	defer o.release()
	if status := secItemDelete(o.itemQuery(origin)); status != errSecSuccess {
		return statusError("delete", status)
	}
	return nil
}
