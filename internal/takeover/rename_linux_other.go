//go:build linux && !amd64 && !arm64

package takeover

const renameat2Syscall = 0 // deliberately unsupported: fail closed
