package easypixel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// Webhook shows scenes on one matrix. It is safe for concurrent use.
type Webhook struct {
	apiKey string
	client *Client
}

// New builds a Webhook for one matrix API key.
func New(apiKey string, opts ...Option) *Webhook {
	cfg := newConfig(opts...)
	return &Webhook{
		apiKey: apiKey,
		client: newClient(cfg),
	}
}

// SendResult is the 202 Accepted body of the webhook endpoint.
type SendResult struct {
	Message  string `json:"message"`
	SceneID  int    `json:"scene_id"`
	MatrixID int    `json:"matrix_id"`
	// VariablesUpdated lists the variables the matrix's scenario actually
	// defines. Names it does not know are dropped without an error, so compare
	// this against what was sent when a value never reaches the panel.
	VariablesUpdated []string `json:"variables_updated"`
}

// Send shows sceneID on the matrix and fills in the scenario's variables.
// The scene must belong to the scenario currently assigned to that matrix,
// otherwise the API answers 422.
//
// The call returns once the API has accepted the request. Rendering and the
// push to the hardware happen afterwards, and there is no delivery
// confirmation.
func (w *Webhook) Send(ctx context.Context, sceneID int, variables map[string]string) (*SendResult, error) {
	body := map[string]any{"scene_id": sceneID}
	if len(variables) > 0 {
		body["variables"] = variables
	}

	raw, err := w.client.Post(ctx, "/api/webhook/"+url.PathEscape(w.apiKey), body)
	if err != nil {
		return nil, err
	}

	var result SendResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("easypixel: decode response: %w", err)
	}

	return &result, nil
}

// APIKey is the matrix API key this Webhook sends with.
func (w *Webhook) APIKey() string { return w.apiKey }

// Client is the underlying transport.
func (w *Webhook) Client() *Client { return w.client }
