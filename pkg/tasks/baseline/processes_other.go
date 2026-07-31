//go:build !linux
// +build !linux

package tasks

import "github.com/chrisns/sandbox-probe/v5/pkg/models"

func getRunningProcessCommandLinux(_ int) (*models.Process, error) { return nil, nil }
func getRunningParentProcessLinux(_ int) (*models.Process, error)  { return nil, nil }
func getRunningProcessesCommandsLinux() ([]*models.Process, error) { return nil, nil }
