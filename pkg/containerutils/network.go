// Package containerutils contains helpers and utilities for managing and creating containers
package containerutils

import (
	"fmt"

	"github.com/89luca89/lilipod/pkg/netns"
	"github.com/89luca89/lilipod/pkg/utils"
)

// setupNetworking configures network namespace for the container if network isolation is requested
func setupNetworking(config utils.Config) (*netns.NetworkNamespace, error) {
	// Only set up network namespace if network isolation is requested
	if config.Network != "private" {
		return nil, nil
	}

	// Create new network namespace instance
	ns, err := netns.New(config.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create network namespace: %w", err)
	}

	// Set up the network namespace
	if err := ns.Setup(); err != nil {
		// Clean up on failure
		_ = ns.Cleanup()
		return nil, fmt.Errorf("failed to set up network namespace: %w", err)
	}

	return ns, nil
}

// cleanupNetworking performs cleanup of network namespace resources
func cleanupNetworking(ns *netns.NetworkNamespace) error {
	if ns == nil {
		return nil
	}

	if err := ns.Cleanup(); err != nil {
		return fmt.Errorf("failed to clean up network namespace: %w", err)
	}

	return nil
}
