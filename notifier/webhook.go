package notifier

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gigcodes/launch-util/config"

	"github.com/gigcodes/launch-util/logger"
)

type Webhook struct {
	Service string

	method      string
	contentType string
	payload     interface{} // Now accepts arbitrary JSON payload
	webhookURL  string      // The URL from config
	headers     map[string]string
}

func NewWebhook(webhook config.WebhookConfig) *Webhook {

	return &Webhook{
		Service:     "Webhook",
		method:      webhook.Method,
		contentType: "application/json",
		payload:     nil, // Initially no payload
		webhookURL:  webhook.Url,
		headers:     webhook.Headers,
	}
}

// Get the logger for this service
func (s *Webhook) getLogger() logger.Logger {
	return logger.Tag(fmt.Sprintf("Notifier: %s", s.Service))
}

// Build the payload, now accepts any JSON payload
func (s *Webhook) buildBody(payload interface{}) ([]byte, error) {
	// Marshal the payload into JSON format
	return json.Marshal(payload)
}

// Notify sends a notification using the webhook URL with arbitrary payload
func (s *Webhook) Notify(payload interface{}) error {
	loggerT := s.getLogger()

	// Use the webhook URL from config
	url := s.webhookURL

	// Build the body with the provided payload
	body, err := s.buildBody(payload)
	if err != nil {
		return err
	}

	loggerT.Infof("Sending notification to %s...", url)
	req, err := http.NewRequest(s.method, url, strings.NewReader(string(body)))
	if err != nil {
		loggerT.Error(err)
		return nil
	}

	// Set the Content-Type for the request
	req.Header.Set("Content-Type", s.contentType)

	// Add custom headers from config
	if s.headers != nil {
		for key, value := range s.headers {
			req.Header.Set(key, value)
		}
	}

	// Create an HTTP client and send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		loggerT.Error(err)
		return nil
	}
	defer resp.Body.Close()

	// Read response body
	var responseBody []byte
	if resp.Body != nil {
		responseBody, err = io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
	}

	// Check the result of the request
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		loggerT.Errorf("Notification failed. Status: %d, Response: %s", resp.StatusCode, string(responseBody))
		return fmt.Errorf("status: %d, body: %s", resp.StatusCode, string(responseBody))
	}

	loggerT.Info("Notification sent successfully.")
	return nil
}
