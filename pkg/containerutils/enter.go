// Package containerutils contains helpers and utilities for managing and creating containers
package containerutils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/89luca89/lilipod/pkg/constants"
	"github.com/89luca89/lilipod/pkg/logging"
	"github.com/89luca89/lilipod/pkg/procutils"
	"github.com/89luca89/lilipod/pkg/utils"
)

// Linux syscall numbers from /usr/include/asm/unistd_64.h
const (
	SYS_UNSHARE = 272
)

// Clone flags from /usr/include/linux/sched.h
const (
	CLONE_NEWNS     = 0x00020000 // New mount namespace
	CLONE_NEWUTS    = 0x04000000 // New utsname namespace
	CLONE_NEWIPC    = 0x08000000 // New ipc namespace
	CLONE_NEWUSER   = 0x10000000 // New user namespace
	CLONE_NEWPID    = 0x20000000 // New pid namespace
	CLONE_NEWNET    = 0x40000000 // New network namespace
	CLONE_NEWCGROUP = 0x02000000 // New cgroup namespace
)

// formatID formats a numeric ID as a string
func formatID(id int) string {
	return strconv.FormatInt(int64(id), 10)
}

// generateEnterCommand creates the command to enter the container with appropriate namespace flags
func generateEnterCommand(config utils.Config) (*exec.Cmd, error) {
	logging.LogDebug("validating config")

	configArg, err := json.Marshal(config)
	if err != nil {
		return nil, errors.New("invalid config")
	}

	// this is our child process that will enter the container effectively
	cmd := exec.Command(os.Args[0],
		"--log-level", logging.GetLogLevel(),
		"enter",
		"--config", string(configArg))

	var cloneFlags uintptr

	// Always create new mount and UTS namespaces
	cloneFlags |= CLONE_NEWNS | CLONE_NEWUTS

	if config.Userns == constants.KeepID &&
		os.Getenv("ROOTFUL") != constants.TrueString {
		cloneFlags |= CLONE_NEWUSER
	}

	if config.Ipc == constants.Private {
		cloneFlags |= CLONE_NEWIPC
	}

	// Set up process attributes for namespace isolation
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Setpgid:    true,
		Foreground: false,
		Credential: &syscall.Credential{
			Uid: 0,
			Gid: 0,
		},
	}

	// Use raw syscall for namespace setup
	if cloneFlags != 0 {
		_, _, errno := syscall.Syscall(SYS_UNSHARE, cloneFlags, 0, 0)
		if errno != 0 {
			return nil, fmt.Errorf("failed to unshare namespaces: %w", errno)
		}
	}

	// Handle network namespace setup
	if config.Network == constants.Private {
		// Set up network namespace using the existing helper
		ns, err := setupNetworking(config)
		if err != nil {
			return nil, err
		}

		// Start slirp4netns for network connectivity
		if err := ns.StartSlirp(os.Getpid()); err != nil {
			// Clean up on failure
			_ = cleanupNetworking(ns)
			return nil, fmt.Errorf("failed to start slirp4netns: %w", err)
		}

		// The network namespace will be cleaned up when the container exits
		// through the cleanupNetworking function
	}

	if config.Pid == constants.Private {
		cloneFlags |= CLONE_NEWPID
	}

	if config.Cgroup == constants.Private {
		cloneFlags |= CLONE_NEWCGROUP
	}

	// Set up user/group credentials
	if config.Userns == constants.KeepID &&
		os.Getenv("ROOTFUL") != constants.TrueString {
		logging.LogDebug("setting up uidmaps")

		uidMaps := config.Uidmap
		if uidMaps == "" {
			logging.LogWarning("cannot find uidMaps, defaulting to 1000:100000:65536")
			uidMaps = "1000:100000:65536"
		}

		logging.LogDebug("setting up gidmaps")

		gidMaps := config.Gidmap
		if gidMaps == "" {
			logging.LogWarning("cannot find gidMaps, defaulting to 1000:100000:65536")
			gidMaps = "1000:100000:65536"
		}

		logging.LogDebug("keep-id passed, setting process UID/GID maps")

		err := procutils.SetProcessKeepIDMaps(cmd, uidMaps, gidMaps)
		if err != nil {
			return nil, err
		}
	}

	return cmd, nil
}

// generateExecCommand will generate an nsenter command to be executed.
// this command will respect the container's namespace configuration and will
// let you execute an entrypoint in target namespace.
func generateExecCommand(containerPid string, tty bool, config utils.Config) *exec.Cmd {
	args := []string{"-m", "-u", "-U", "--preserve-credentials"}

	if config.Ipc == constants.Private {
		args = append(args, "-i")
	}

	if config.Network == constants.Private {
		args = append(args, "-n")
	}

	if config.Pid == constants.Private {
		args = append(args, "-p")
	}

	uid, gid := procutils.GetUIDGID(config.User)

	args = append(args, []string{"-S", formatID(uid)}...)
	args = append(args, []string{"-G", formatID(gid)}...)
	args = append(args, []string{fmt.Sprintf("-r/proc/%s/root", containerPid)}...)
	args = append(args, []string{fmt.Sprintf("-w/proc/%s/root/%s", containerPid, config.Workdir)}...)
	args = append(args, []string{"-t", containerPid}...)

	logging.LogDebug("nsenter flags: %v", args)

	if tty {
		logging.LogDebug(
			"tty requested, execute command with agent: %s %v",
			constants.PtyAgentPath,
			config.Entrypoint,
		)

		args = append(args, []string{constants.PtyAgentPath}...)
	}

	args = append(args, config.Entrypoint...)

	logging.LogDebug("executing nsenter: %s %v", "nsenter", args)

	cmd := exec.Command("nsenter", args...)
	cmd.Env = config.Env

	return cmd
}
