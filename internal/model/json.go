package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON[T] is a value kept as JSON in a text column. It is bound as a string,
// never as bytes: PostgreSQL would read bytes into bytea's escape format and
// mangle quotes and backslashes (the panel's lesson).
type JSON[T any] struct{ V T }

// Value writes the JSON text.
func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan reads the JSON text; an empty column is the zero value.
func (j *JSON[T]) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		var zero T
		j.V = zero
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("JSON: cannot read %T", src)
	}
	if len(b) == 0 {
		var zero T
		j.V = zero
		return nil
	}
	return json.Unmarshal(b, &j.V)
}

// MarshalJSON is the value itself, not the wrapper.
func (j JSON[T]) MarshalJSON() ([]byte, error) { return json.Marshal(j.V) }

// UnmarshalJSON reads the value itself.
func (j *JSON[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.V) }

// GormDataType keeps the column text on both databases.
func (JSON[T]) GormDataType() string { return "text" }
