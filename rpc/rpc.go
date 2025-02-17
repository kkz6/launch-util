package rpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/gigcodes/launch-util/config"
	"github.com/gigcodes/launch-util/logger"
	"github.com/gigcodes/launch-util/notifier"
	"github.com/kolo/xmlrpc"
)

// ProcessInfo represents the structure returned by Supervisor for a process.
type ProcessInfo struct {
	Name        string `xmlrpc:"name" json:"name"`
	Group       string `xmlrpc:"group" json:"group"`
	Description string `xmlrpc:"description" json:"description"`
	Start       int64  `xmlrpc:"start" json:"start"`
	Stop        int64  `xmlrpc:"stop" json:"stop"`
	Now         int64  `xmlrpc:"now" json:"now"`
	Statename   string `xmlrpc:"statename" json:"statename"`
	State       int    `xmlrpc:"state" json:"state"`
	Spawnerr    string `xmlrpc:"spawnerr" json:"spawnerr"`
	Uptime      int64  `json:"uptime"` // Process-specific uptime
}

// DaemonStatus is the structure used for each daemon's status.
type DaemonStatus struct {
	DaemonID    string        `json:"daemon_id"`
	Status      string        `json:"status"`
	Error       string        `json:"error,omitempty"`
	Group       string        `json:"group,omitempty"`
	Statename   string        `json:"statename,omitempty"`
	Description string        `json:"description,omitempty"`
	Processes   []ProcessInfo `json:"processes"`    // List of processes in the group
	Uptime      int64         `json:"total_uptime"` // Highest uptime in the group
}

// newUnixSocketXMLRPCClient creates a new XML-RPC client that communicates over a Unix socket.
func newUnixSocketXMLRPCClient(socketPath, rpcEndpoint string) (*xmlrpc.Client, error) {
	transport := &http.Transport{
		Dial: func(network, addr string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
		// You can also set timeouts or other transport options if desired.
	}
	client, err := xmlrpc.NewClient(rpcEndpoint, transport)
	if err != nil {
		return nil, fmt.Errorf("error creating XML-RPC client: %w", err)
	}
	return client, nil
}

// getSupervisorProcessInfo connects to Supervisor's XML-RPC endpoint via Unix socket and retrieves process info.
func getSupervisorProcessInfo(socketPath, rpcEndpoint, processID string) (*ProcessInfo, error) {
	client, err := newUnixSocketXMLRPCClient(socketPath, rpcEndpoint)
	if err != nil {
		return nil, err
	}
	var info ProcessInfo
	err = client.Call("supervisor.getProcessInfo", []interface{}{processID}, &info)
	if err != nil {
		return nil, fmt.Errorf("error calling getProcessInfo: %w", err)
	}
	return &info, nil
}

func sendStatusWebhook(payload map[string]interface{}) error {
	webhook := notifier.NewWebhook(config.Pulse.Webhook)
	return webhook.Notify(payload)
}

func getAllSupervisorProcessInfo(socketPath, rpcEndpoint string) ([]ProcessInfo, error) {
	client, err := newUnixSocketXMLRPCClient(socketPath, rpcEndpoint)
	if err != nil {
		return nil, err
	}
	var infos []ProcessInfo
	err = client.Call("supervisor.getAllProcessInfo", nil, &infos)
	if err != nil {
		return nil, fmt.Errorf("error calling getAllProcessInfo: %w", err)
	}
	return infos, nil
}

// SendDaemonStatus checks the status for each given daemon ID using Supervisor via Unix socket,
// prepares a payload with event "d_stat" and an array of daemon statuses (including uptime and extra info),
// and sends it via your configured webhook.
// The parameters socketPath and rpcEndpoint must be provided. For example:
//
//	socketPath: "/var/run/supervisor.sock"
//	rpcEndpoint: "http://localhost/RPC2"
func SendDaemonStatus(daemonIDs []string, socketPath, rpcEndpoint string) error {
	statusMap := make(map[string]*DaemonStatus)

	// If no daemon IDs are provided, retrieve status for all daemons.
	if len(daemonIDs) == 0 {
		allInfos, err := getAllSupervisorProcessInfo(socketPath, rpcEndpoint)
		if err != nil {
			return fmt.Errorf("failed to retrieve all process info: %w", err)
		}
		for _, info := range allInfos {
			updateDaemonGroupStatus(statusMap, &info)
		}
	} else {
		// Retrieve status for the provided daemon IDs.
		for _, id := range daemonIDs {
			info, err := getSupervisorProcessInfo(socketPath, rpcEndpoint, id)
			if err != nil {
				log.Printf("Error retrieving process info for %s: %v", id, err)
				statusMap[id] = &DaemonStatus{
					DaemonID:  id,
					Status:    "not_running",
					Error:     err.Error(),
					Processes: []ProcessInfo{},
				}
				continue
			}
			updateDaemonGroupStatus(statusMap, info)
		}
	}

	// Convert map to slice
	var statuses []DaemonStatus
	for _, status := range statusMap {
		// Determine the final group-level status
		runningCount := 0
		for _, p := range status.Processes {
			if p.State == 20 { // RUNNING
				runningCount++
			}
		}

		if runningCount == len(status.Processes) {
			status.Status = "fully_running"
		} else if runningCount > 0 {
			status.Status = "partially_running"
		} else {
			status.Status = "not_running"
		}

		statuses = append(statuses, *status)
	}

	payload := map[string]interface{}{
		"event": "d_stat",
		"data":  statuses,
	}

	payloadJSON, _ := json.Marshal(payload)
	log.Printf("Sending grouped payload: %s", payloadJSON)

	if err := sendStatusWebhook(payload); err != nil {
		return fmt.Errorf("error sending webhook: %w", err)
	}

	logger.Infof("Grouped supervisor statuses by Group sent successfully: %+v", statuses)
	return nil
}

// updateDaemonGroupStatus groups the statuses of processes by their group and adds process details.
func updateDaemonGroupStatus(statusMap map[string]*DaemonStatus, info *ProcessInfo) {
	if info.Group == "" {
		info.Group = "unknown" // Handle cases where group is missing
	}

	if _, exists := statusMap[info.Group]; !exists {
		statusMap[info.Group] = &DaemonStatus{
			DaemonID:    info.Group, // Use the group name as the daemon ID
			Status:      "not_running",
			Uptime:      0,
			Error:       "",
			Group:       info.Group,
			Statename:   info.Statename,
			Description: info.Description,
			Processes:   []ProcessInfo{},
		}
	}

	status := statusMap[info.Group]

	// Calculate uptime for this specific process
	uptime := int64(0)
	if info.State == 20 && info.Start > 0 {
		uptime = info.Now - info.Start
	}

	// Store process details with calculated uptime
	info.Uptime = uptime
	status.Processes = append(status.Processes, *info)

	if uptime > status.Uptime {
		status.Uptime = uptime
	}
}
