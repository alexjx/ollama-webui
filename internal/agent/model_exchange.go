package agent

import (
	"encoding/json"

	"ollama-webui/internal/ollama"
)

// ModelExchange records only the latest model call for an assistant reply.
// Raw JSON snapshots keep later context mutations out of the inspected request.
type ModelExchange struct {
	Version  int             `json:"version"`
	Turn     int             `json:"turn"`
	Request  json.RawMessage `json:"request"`
	Response ollama.Message  `json:"response"`
	Status   string          `json:"status"`
	Error    string          `json:"error,omitempty"`
}
