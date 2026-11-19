//go:build windows

package clip

import (
	"bytes"
	"testing"
	"unsafe"
)

// 这两条用例盯的是 vet 那 4 条 unsafeptr 警告改到的代码路径，而且**不碰真实剪贴板**：
// 一条用 HGLOBAL 全局内存验 globalBytes，一条用假 COM 对象验虚表取槽。
// 因此它们不需要 requireIdleClipboard —— ClipNest 正在运行时也照跑。

func TestGlobalBytesCopiesHGLOBAL(t *testing.T) {
	const size = 64
	h, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		t.Fatalf("GlobalAlloc: %v", err)
	}
	free := kernel32.NewProc("GlobalFree")
	defer free.Call(h)

	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		t.Fatal("GlobalLock failed")
	}
	want := bytes.Repeat([]byte{0x7f}, size)
	copy(unsafe.Slice((*byte)(ptrFromUintptr(ptr)), size), want)
	procGlobalUnlock.Call(h)

	// GlobalSize 可能比请求的大（堆按粒度取整），只比较请求的那段
	got, err := globalBytes(h)
	if err != nil {
		t.Fatalf("globalBytes: %v", err)
	}
	if len(got) < size {
		t.Fatalf("globalBytes 长度 = %d, want >= %d", len(got), size)
	}
	if !bytes.Equal(got[:size], want) {
		t.Fatalf("globalBytes 内容不对：前 8 字节 % x", got[:8])
	}
}

func TestVtableMethodReadsSlot(t *testing.T) {
	// 假 COM 对象：对象头第一个字段指向一张虚表，虚表里放几个可辨认的值。
	slots := []uintptr{0x1111, 0x2222, 0x3333}
	type fakeObject struct{ vtbl unsafe.Pointer }
	obj := fakeObject{vtbl: unsafe.Pointer(&slots[0])}

	for i, want := range slots {
		if got := vtableMethod(unsafe.Pointer(&obj), i); got != want {
			t.Errorf("vtableMethod(%d) = %#x, want %#x", i, got, want)
		}
	}
}
