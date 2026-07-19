package agentipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Pimeng/gooophira-mp/internal/agentproto"
)

func ReadDiscovery(path string) (agentproto.Discovery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agentproto.Discovery{}, err
	}
	var discovery agentproto.Discovery
	if err := json.Unmarshal(data, &discovery); err != nil {
		return agentproto.Discovery{}, fmt.Errorf("agent IPC: decode discovery: %w", err)
	}
	if discovery.Endpoint == "" || discovery.Token == "" {
		return agentproto.Discovery{}, errors.New("agent IPC: incomplete discovery file")
	}
	return discovery, nil
}

func writeDiscovery(path string, discovery agentproto.Discovery) (func() error, error) {
	data, err := json.MarshalIndent(discovery, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".agent-ipc-*")
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return nil, err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	if err := temp.Sync(); err != nil {
		return nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	if err := replaceFile(tempPath, path); err != nil {
		return nil, err
	}
	removeTemp = false
	if err := restrictFileToCurrentUser(path); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent IPC: restrict discovery file: %w", err)
	}
	created, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return func() error {
		current, statErr := os.Stat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if !os.SameFile(created, current) {
			return nil
		}
		return os.Remove(path)
	}, nil
}
