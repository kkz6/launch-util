package rpc

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/gigcodes/launch-util/config"
	"github.com/gigcodes/launch-util/logger"
	"github.com/gigcodes/launch-util/notifier"
	"github.com/kolo/xmlrpc"
)

// ProcessInfo represents the structure returned by Supervisor for a process.
type ProcessInfo struct {
	Name      string `xmlrpc:"name"`
	Group     string `xmlrpc:"group"`
	Statename string `xmlrpc:"statename"`
	State     int    `xmlrpc:"state"`
	Spawnerr  string `xmlrpc:"spawnerr"`
}

// DaemonStatus is the structure used for each daemon's status.
type DaemonStatus struct {
	DaemonID string `json:"daemon_id"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// getSupervisorProcessInfo connects to Supervisor's XML-RPC endpoint and retrieves process info.
func getSupervisorProcessInfo(supervisorURL, processID string) (*ProcessInfo, error) {
	client, err := xmlrpc.NewClient(supervisorURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating XML-RPC client: %w", err)
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

// SendDaemonStatus checks the status for each given daemon ID using Supervisor,
// prepares a payload with event "d_stat" and the array of daemon statuses,
// and sends it via your configured webhook.
func SendDaemonStatus(daemonIDs []string, supervisorURL string) error {
	var statuses []DaemonStatus

	for _, id := range daemonIDs {
		var status string
		var errMsg string

		info, err := getSupervisorProcessInfo(supervisorURL, id)
		if err != nil {
			status = "not_running"
			errMsg = err.Error()
			log.Printf("Error retrieving process info for %s: %v", id, err)
		} else {
			// Supervisor defines state 20 as RUNNING.
			if info.State == 20 {
				status = "running"
			} else {
				status = "not_running"
				errMsg = fmt.Sprintf("Process state: %s", info.Statename)
			}
		}
		statuses = append(statuses, DaemonStatus{
			DaemonID: id,
			Status:   status,
			Error:    errMsg,
		})
	}

	payload := map[string]interface{}{
		"event": "d_stat",
		"data":  statuses,
	}

	// Optionally, you could log the payload as JSON.
	payloadJSON, _ := json.Marshal(payload)
	log.Printf("Sending payload: %s", payloadJSON)

	if err := sendStatusWebhook(payload); err != nil {
		return fmt.Errorf("error sending webhook: %w", err)
	}

	logger.Infof("Supervisor statuses sent successfully: %+v", statuses)
	return nil
}
