package rpc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/longbridgeapp/assert"
)

// TestUpdateDaemonGroupStatus validates that processes are grouped correctly and uptime is assigned properly.
func TestUpdateDaemonGroupStatus(t *testing.T) {
	statusMap := make(map[string]*DaemonStatus)

	// Simulated process info
	process1 := &ProcessInfo{
		Name:      "worker-1",
		Group:     "worker-group",
		Start:     1700000000,
		Now:       1700005000,
		State:     20, // Running
		Statename: "RUNNING",
	}

	process2 := &ProcessInfo{
		Name:      "worker-2",
		Group:     "worker-group",
		Start:     1699998000,
		Now:       1700005000,
		State:     20, // Running
		Statename: "RUNNING",
	}

	process3 := &ProcessInfo{
		Name:      "worker-3",
		Group:     "worker-group",
		Start:     0, // Never started
		Now:       1700005000,
		State:     0, // Stopped
		Statename: "STOPPED",
	}

	// Update group status with all three processes
	updateDaemonGroupStatus(statusMap, process1)
	updateDaemonGroupStatus(statusMap, process2)
	updateDaemonGroupStatus(statusMap, process3)

	// Assert that the worker-group exists in the map
	assert.True(t, statusMap["worker-group"] != nil)

	// The highest uptime should be taken (process2 has the highest uptime)
	assert.Equal(t, int64(7034), statusMap["worker-group"].Uptime)

	// Ensure all processes are stored in the group
	assert.Equal(t, len(statusMap["worker-group"].Processes), 3)

	// Verify correct grouping of processes
	assert.Equal(t, statusMap["worker-group"].Processes[0].Name, "worker-1")
	assert.Equal(t, statusMap["worker-group"].Processes[1].Name, "worker-2")
	assert.Equal(t, statusMap["worker-group"].Processes[2].Name, "worker-3")
}

// TestSendDaemonStatus_NoDaemonIDs ensures it correctly processes all daemons when no IDs are provided.
func TestSendDaemonStatus_NoDaemonIDs(t *testing.T) {
	// Mock the getAllSupervisorProcessInfo function
	getAllSupervisorProcessInfo = func(socketPath, rpcEndpoint string) ([]ProcessInfo, error) {
		return []ProcessInfo{
			{Name: "worker-1", Group: "worker-group", Start: 1700000000, Now: 1700005000, State: 20},
			{Name: "worker-2", Group: "worker-group", Start: 1699998000, Now: 1700005000, State: 20},
			{Name: "web-server-1", Group: "web-server-group", Start: 0, Now: 1700005000, State: 0},
		}, nil
	}

	// Call function
	err := SendDaemonStatus(nil, "/var/run/supervisor.sock", "http://localhost/RPC2")

	// Validate results
	assert.Nil(t, err)
}

// TestSendDaemonStatus_WithDaemonIDs ensures correct processing when daemon IDs are provided.
func TestSendDaemonStatus_WithDaemonIDs(t *testing.T) {
	// Mock getSupervisorProcessInfo
	getSupervisorProcessInfo = func(socketPath, rpcEndpoint, processID string) (*ProcessInfo, error) {
		if processID == "worker-1" {
			return &ProcessInfo{Name: "worker-1", Group: "worker-group", Start: 1700000000, Now: 1700005000, State: 20}, nil
		}
		if processID == "worker-2" {
			return &ProcessInfo{Name: "worker-2", Group: "worker-group", Start: 1699998000, Now: 1700005000, State: 20}, nil
		}
		return nil, errors.New("Process not found")
	}

	// Call function with specific daemon IDs
	err := SendDaemonStatus([]string{"worker-1", "worker-2"}, "/var/run/supervisor.sock", "http://localhost/RPC2")

	// Validate results
	assert.Nil(t, err)
}

// TestSendDaemonStatus_ErrorHandling ensures the function handles errors properly.
func TestSendDaemonStatus_ErrorHandling(t *testing.T) {
	// Mock getSupervisorProcessInfo to return an error
	getSupervisorProcessInfo = func(socketPath, rpcEndpoint, processID string) (*ProcessInfo, error) {
		return nil, fmt.Errorf("Failed to fetch process info")
	}

	// Call function with a non-existing daemon ID
	err := SendDaemonStatus([]string{"non-existing"}, "/var/run/supervisor.sock", "http://localhost/RPC2")

	// Validate that an error is returned
	assert.NotNil(t, err)
}
