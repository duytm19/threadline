package catalog

import (
	"encoding/json"
)

// Attributes wraps dynamic JSONB product and variant attributes.
type Attributes struct {
	data map[string]any
}

// NewAttributes constructs an immutable Attributes value object.
func NewAttributes(data map[string]any) Attributes {
	if data == nil {
		return Attributes{data: make(map[string]any)}
	}
	copied := make(map[string]any, len(data))
	for k, v := range data {
		copied[k] = v
	}
	return Attributes{data: copied}
}

// Map returns a defensive copy of the attributes map.
func (a Attributes) Map() map[string]any {
	if a.data == nil {
		return make(map[string]any)
	}
	copied := make(map[string]any, len(a.data))
	for k, v := range a.data {
		copied[k] = v
	}
	return copied
}

// GetString retrieves a string attribute.
func (a Attributes) GetString(key string) (string, bool) {
	if a.data == nil {
		return "", false
	}
	val, ok := a.data[key]
	if !ok {
		return "", false
	}
	s, ok := val.(string)
	return s, ok
}

// GetInt retrieves an integer attribute.
func (a Attributes) GetInt(key string) (int64, bool) {
	if a.data == nil {
		return 0, false
	}
	val, ok := a.data[key]
	if !ok {
		return 0, false
	}
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	}
	return 0, false
}

// GetBool retrieves a boolean attribute.
func (a Attributes) GetBool(key string) (bool, bool) {
	if a.data == nil {
		return false, false
	}
	val, ok := a.data[key]
	if !ok {
		return false, false
	}
	b, ok := val.(bool)
	return b, ok
}

// MarshalJSON serializes attributes into JSON bytes.
func (a Attributes) MarshalJSON() ([]byte, error) {
	if a.data == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(a.data)
}

// UnmarshalJSON deserializes JSON bytes into Attributes.
func (a *Attributes) UnmarshalJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	a.data = m
	return nil
}
