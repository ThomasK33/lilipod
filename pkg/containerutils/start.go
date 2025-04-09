// Package containerutils contains helpers and utilities for managing and creating containers
package containerutils

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/89luca89/lilipod/pkg/constants"
	"github.com/89luca89/lilipod/pkg/fileutils"
	"github.com/89luca89/lilipod/pkg/logging"
	"github.com/89luca89/lilipod/pkg/netns"
	"github.com/89luca89/lilipod/pkg/procutils"
	"github.com/89luca89/lilipod/pkg/utils"
)

// Start will enter the target container.
// If tty is specified, the container will be started in interactive mode with full shell.
// If interactive only is specified, container will be started in interactive mode, but only stdin will be forwarded.
// Else the container will be started in background and all output will be saved in the logs.
func Start(interactive, tty bool, config utils.Config) error {
	logging.LogDebug("entering container")

	path := GetRootfsDir(config.ID)

	logging.LogDebug("searching pty agent")

	ptyFile, err := fileutils.ReadFile(filepath.Join(utils.LilipodBinPath, "pty"))
	if err != nil {
		logging.LogError("failed to read pty agent: %v", err)
		return err
	}

	if !fileutils.Exist(filepath.Join(path, constants.PtyAgentPath)) {
		logging.LogDebug("injecting pty agent")

		err = os.MkdirAll(filepath.Join(path, filepath.Base(constants.PtyAgentPath)), 0o755)
		if err != nil {
			logging.LogError("failed to create path for pty agent: %v", err)
			return err
		}

		err = fileutils.WriteFile(filepath.Join(path, constants.PtyAgentPath), ptyFile, 0o755)
		if err != nil {
			logging.LogError("failed to inject pty agent: %v", err)
			return err
		}

		logging.LogDebug("pty agent injected")
	}

	if !fileutils.Exist(filepath.Join(path, constants.PtyAgentPath)) {
		logging.LogError(
			"failed to inject agent in %s",
			filepath.Join(path, constants.PtyAgentPath),
		)

		return fmt.Errorf(
			"failed to inject agent in %s",
			filepath.Join(path, constants.PtyAgentPath),
		)
	}

	logging.LogDebug("ready to start the container")

	// Set up network namespace if network isolation is requested
	var ns *netns.NetworkNamespace
	if config.Network == "private" {
		logging.LogDebug("setting up network namespace")
		ns, err = setupNetworking(config)
		if err != nil {
			logging.LogError("failed to set up network namespace: %v", err)
			return err
		}
		// Ensure cleanup on any error
		defer func() {
			if err != nil {
				_ = cleanupNetworking(ns)
			}
		}()
	}

	cmd, err := generateEnterCommand(config)
	if err != nil {
		logging.LogError("failed to generate enter cmd: %v", err)
		return err
	}

	logging.LogDebug("container is starting with %+v", cmd.SysProcAttr)
	logging.LogDebug("starting the container, executing %v", cmd.Args)

	// Start the container process
	var startErr error
	if tty {
		cmd.Args = append(cmd.Args, "--tty")
		startErr = procutils.RunWithTTY(cmd)
	} else if interactive {
		startErr = procutils.RunInteractive(cmd)
	} else {
		logfile := filepath.Join(path, "../current-logs")
		startErr = procutils.RunDetached(cmd, logfile)
	}

	// If network namespace was created, start slirp4netns after the container process
	if ns != nil {
		pid, err := GetPid(config.ID)
		if err != nil {
			logging.LogError("failed to get container PID: %v", err)
			return fmt.Errorf("failed to get container PID: %w", err)
		}

		if err := ns.StartSlirp(pid); err != nil {
			logging.LogError("failed to start slirp4netns: %v", err)
			return fmt.Errorf("failed to start slirp4netns: %w", err)
		}
	}

	// Return any error from starting the container
	return startErr
}
