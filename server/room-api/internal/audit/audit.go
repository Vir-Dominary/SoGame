package audit

import (
	"encoding/json"
	"log"
	"time"
)

func Event(name string, fields map[string]any) {
	payload := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "event": name}
	for key, value := range fields {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf(`{"event":"audit_encode_error"}`)
		return
	}
	log.Print(string(encoded))
}