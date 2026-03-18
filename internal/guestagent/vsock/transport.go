package vsock

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"
)

// Dial opens a vsock connection to the host on the given port.
// Uses raw syscalls — stdlib net package has no AF_VSOCK support.
func Dial(cid, port uint32) (net.Conn, error) {
	fd, err := syscall.Socket(AFVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}

	addr := SockaddrVM{
		Family: AFVsock,
		Port:   port,
		CID:    cid,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Sizeof(addr)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("connect(vsock cid=%d port=%d): %v", cid, port, errno)
	}

	// Wrap the raw fd as a net.Conn using os.File
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port))
	conn, err := net.FileConn(f)
	f.Close() // FileConn dups the fd, so close the original
	if err != nil {
		return nil, fmt.Errorf("net.FileConn: %w", err)
	}
	return conn, nil
}

// Listen creates a vsock server socket bound to the given port.
func Listen(port uint32) (net.Listener, error) {
	fd, err := syscall.Socket(AFVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}

	addr := SockaddrVM{
		Family: AFVsock,
		Port:   port,
		CID:    VMAddrCIDAny,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_BIND,
		uintptr(fd),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Sizeof(addr)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind(vsock port=%d): %w", port, errno)
	}

	if err := syscall.Listen(fd, 1); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("listen(vsock port=%d): %w", port, err)
	}

	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock-listen:%d", port))
	ln, err := net.FileListener(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("net.FileListener: %w", err)
	}
	return ln, nil
}
