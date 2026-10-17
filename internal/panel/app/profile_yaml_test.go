package app

import (
	"encoding/json"

	"go.yaml.in/yaml/v3"
)

// unmarshalProfile reads a Clash profile, which is YAML, into a struct with json tags.
func unmarshalProfile(raw []byte, v any) error {
	var m any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
