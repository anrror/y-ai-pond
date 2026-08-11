package controller

import (
	"context"
	"encoding/json"

	"github.com/anrror/y-ai-pond/pkg/mqtt"
)

// MQTTReporter publishes StatusReport JSON via the MQTT client.
// Heartbeats are published to statusTopic; feeding decisions to decisionTopic.
type MQTTReporter struct {
	client        *mqtt.Client
	statusTopic   string
	decisionTopic string
}

// NewMQTTReporter creates an MQTT-backed status reporter.
func NewMQTTReporter(client *mqtt.Client, statusTopic, decisionTopic string) *MQTTReporter {
	return &MQTTReporter{
		client:        client,
		statusTopic:   statusTopic,
		decisionTopic: decisionTopic,
	}
}

// Report marshals the status report to JSON and publishes it via MQTT.
// Decision reports use decisionTopic; all others use statusTopic.
func (r *MQTTReporter) Report(ctx context.Context, s StatusReport) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	topic := r.statusTopic
	if s.Decision {
		topic = r.decisionTopic
	}
	return r.client.PublishCommand(ctx, topic, data)
}
