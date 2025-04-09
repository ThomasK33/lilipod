// Package netns provides network namespace management functionality for lilipod
package netns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"github.com/89luca89/lilipod/pkg/utils"
	"golang.org/x/sys/unix"
)

const (
	// Linux syscall numbers from /usr/include/asm/unistd_64.h
	SYS_UNSHARE = 272
	SYS_SETNS   = 308
	SYS_UMOUNT2 = 166

	// Clone flags from /usr/include/linux/sched.h
	CLONE_NEWNET = 0x40000000

	// Mount flags from /usr/include/linux/mount.h
	MS_BIND = 4096
)

// NetworkNamespace represents a network namespace configuration
type NetworkNamespace struct {
	ContainerID    string
	RuntimeDir     string
	NetNSMountPath string
	SlirpAPISocket string
	slirpProcess   *os.Process
}

// New creates a new NetworkNamespace instance
func New(containerID string) (*NetworkNamespace, error) {
	// Create runtime directory for this container
	runtimeDir := filepath.Join("/run/user", fmt.Sprint(os.Getuid()), "lilipod", containerID)
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create runtime directory: %w", err)
	}

	return &NetworkNamespace{
		ContainerID:    containerID,
		RuntimeDir:     runtimeDir,
		NetNSMountPath: filepath.Join(runtimeDir, "netns"),
		SlirpAPISocket: filepath.Join(runtimeDir, "slirp.sock"),
	}, nil
}

// Setup creates and configures the network namespace
func (n *NetworkNamespace) Setup() error {
	// Create new network namespace
	// Use raw syscall for unshare since it's not directly exposed in unix package
	_, _, errno := unix.Syscall(SYS_UNSHARE, uintptr(CLONE_NEWNET), 0, 0)
	if errno != 0 {
		return fmt.Errorf("failed to unshare network namespace: %w", errno)
	}

	// Get the current process's network namespace path
	netnsProcPath := fmt.Sprintf("/proc/%d/ns/net", os.Getpid())

	// Create an empty file as mount point
	if err := os.WriteFile(n.NetNSMountPath, []byte{}, 0600); err != nil {
		return fmt.Errorf("failed to create netns mount point: %w", err)
	}

	// Use raw syscall for mount since the unix.Mount signature doesn't match what we need
	mountFlags := MS_BIND
	_, _, errno = unix.Syscall6(unix.SYS_MOUNT,
		uintptr(unsafe.Pointer(&[]byte(netnsProcPath)[0])),
		uintptr(unsafe.Pointer(&[]byte(n.NetNSMountPath)[0])),
		uintptr(unsafe.Pointer(&[]byte("none")[0])),
		uintptr(mountFlags),
		0, 0)
	if errno != 0 {
		os.Remove(n.NetNSMountPath)
		return fmt.Errorf("failed to bind mount network namespace: %w", errno)
	}

	return nil
}

// StartSlirp starts the slirp4netns process for the given target PID
func (n *NetworkNamespace) StartSlirp(targetPid int) error {
	// Construct the path to the slirp4netns binary managed by EnsureUNIXDependencies
	slirpPath := filepath.Join(utils.LilipodBinPath, "slirp4netns")

	// Check if the binary exists
	if _, err := os.Stat(slirpPath); err != nil {
		return fmt.Errorf("slirp4netns binary not found at %s, ensure dependencies are set up: %w", slirpPath, err)
	}

	// Prepare slirp4netns command
	cmd := exec.Command(slirpPath,
		"--configure",
		"--mtu=65520",
		"-r", "/etc/resolv.conf",
		"-a", n.SlirpAPISocket,
		fmt.Sprint(targetPid),
		"tap0",
	)

	// Start the slirp4netns process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start slirp4netns: %w", err)
	}

	// Store the process for later cleanup
	n.slirpProcess = cmd.Process

	return nil
}

// Cleanup performs cleanup of the network namespace and associated resources
func (n *NetworkNamespace) Cleanup() error {
	var errors []error

	// Terminate slirp4netns process if it exists
	if n.slirpProcess != nil {
		// Try SIGTERM first
		if err := n.slirpProcess.Signal(unix.SIGTERM); err != nil {
			errors = append(errors, fmt.Errorf("failed to send SIGTERM to slirp4netns: %w", err))
			// Force kill if SIGTERM fails
			if err := n.slirpProcess.Kill(); err != nil {
				errors = append(errors, fmt.Errorf("failed to kill slirp4netns: %w", err))
			}
		}
		// Wait for the process to exit
		_, _ = n.slirpProcess.Wait()
	}

	// Unmount the network namespace using raw syscall
	_, _, errno := unix.Syscall(SYS_UMOUNT2,
		uintptr(unsafe.Pointer(&[]byte(n.NetNSMountPath)[0])),
		0, 0)
	if errno != 0 {
		errors = append(errors, fmt.Errorf("failed to unmount network namespace: %w", errno))
	}

	// Remove the netns mount file
	if err := os.Remove(n.NetNSMountPath); err != nil {
		errors = append(errors, fmt.Errorf("failed to remove netns mount point: %w", err))
	}

	// Remove the API socket if it exists
	if err := os.Remove(n.SlirpAPISocket); err != nil && !os.IsNotExist(err) {
		errors = append(errors, fmt.Errorf("failed to remove slirp API socket: %w", err))
	}

	// Remove the runtime directory
	if err := os.RemoveAll(n.RuntimeDir); err != nil {
		errors = append(errors, fmt.Errorf("failed to remove runtime directory: %w", err))
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %v", errors)
	}
	return nil
}

// SetupChildNetworking sets up networking in the child process
func SetupChildNetworking(netnsPath string) error {
	// Open the network namespace
	netnsFd, err := unix.Open(netnsPath, unix.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open network namespace: %w", err)
	}
	defer unix.Close(netnsFd)

	// Use raw syscall for setns since it's not directly exposed in unix package
	_, _, errno := unix.Syscall(SYS_SETNS, uintptr(netnsFd), uintptr(CLONE_NEWNET), 0)
	if errno != 0 {
		return fmt.Errorf("failed to enter network namespace: %w", errno)
	}

	// Configure loopback interface
	// Note: In a real implementation, you might want to use netlink for this
	// instead of executing the ip command
	cmd := exec.Command("ip", "link", "set", "lo", "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure loopback interface: %w", err)
	}

	return nil
}
