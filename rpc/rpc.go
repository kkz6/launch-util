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
	Name        string `xmlrpc:"name"`
	Group       string `xmlrpc:"group"`
	Description string `xmlrpc:"description"`
	Start       int64  `xmlrpc:"start"`
	Stop        int64  `xmlrpc:"stop"`
	Now         int64  `xmlrpc:"now"`
	Statename   string `xmlrpc:"statename"`
	State       int    `xmlrpc:"state"`
	Spawnerr    string `xmlrpc:"spawnerr"`
}

// DaemonStatus is the structure used for each daemon's status.
type DaemonStatus struct {
	DaemonID    string `json:"daemon_id"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Uptime      int64  `json:"uptime,omitempty"` // seconds
	Group       string `json:"group,omitempty"`
	Statename   string `json:"statename,omitempty"`
	Description string `json:"description,omitempty"`
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
	var statuses []DaemonStatus

	// If no daemon IDs are provided, retrieve status for all daemons.
	if len(daemonIDs) == 0 {
		allInfos, err := getAllSupervisorProcessInfo(socketPath, rpcEndpoint)
		if err != nil {
			return fmt.Errorf("failed to retrieve all process info: %w", err)
		}
		for _, info := range allInfos {
			var status string
			var errMsg string
			var uptime int64
			if info.State == 20 { // Supervisor defines state 20 as RUNNING.
				status = "running"
				if info.Start > 0 {
					uptime = info.Now - info.Start
				}
			} else {
				status = "not_running"
				errMsg = fmt.Sprintf("Process state: %s", info.Statename)
			}
			statuses = append(statuses, DaemonStatus{
				DaemonID:    info.Name, // Using the process name as the daemon id.
				Status:      status,
				Error:       errMsg,
				Uptime:      uptime,
				Group:       info.Group,
				Statename:   info.Statename,
				Description: info.Description,
			})
		}
	} else {
		// Retrieve status for the provided daemon IDs.
		for _, id := range daemonIDs {
			var status string
			var errMsg string
			var uptime int64

			info, err := getSupervisorProcessInfo(socketPath, rpcEndpoint, id)
			if err != nil {
				status = "not_running"
				errMsg = err.Error()
				log.Printf("Error retrieving process info for %s: %v", id, err)
				statuses = append(statuses, DaemonStatus{
					DaemonID: id,
					Status:   status,
					Error:    errMsg,
				})
				continue
			}

			if info.State == 20 {
				status = "running"
				if info.Start > 0 {
					uptime = info.Now - info.Start
				}
			} else {
				status = "not_running"
				errMsg = fmt.Sprintf("Process state: %s", info.Statename)
			}

			statuses = append(statuses, DaemonStatus{
				DaemonID:    id,
				Status:      status,
				Error:       errMsg,
				Uptime:      uptime,
				Group:       info.Group,
				Statename:   info.Statename,
				Description: info.Description,
			})
		}
	}

	payload := map[string]interface{}{
		"event": "d_stat",
		"data":  statuses,
	}

	payloadJSON, _ := json.Marshal(payload)
	log.Printf("Sending payload: %s", payloadJSON)

	if err := sendStatusWebhook(payload); err != nil {
		return fmt.Errorf("error sending webhook: %w", err)
	}

	logger.Infof("Supervisor statuses sent successfully: %+v", statuses)
	return nil
}
