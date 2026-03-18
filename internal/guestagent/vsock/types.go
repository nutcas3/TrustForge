package vsock

// vsock address family constant (not in stdlib)
const (
	AFVsock       = 40
	VMAddrCIDHost = 2  // the hypervisor/host
	VMAddrCIDAny  = 0xFFFFFFFF
	CommandPort   = 52
	ReadyPort     = 53
)

// sockaddrVM is the raw vsock sockaddr for syscall.Bind / syscall.Connect
type SockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Pad       [4]byte
}
