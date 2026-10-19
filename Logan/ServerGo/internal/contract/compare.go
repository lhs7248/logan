package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

func decodeJSON(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return value, nil
}
func CompareJSON(a, b []byte) error {
	left, err := decodeJSON(a)
	if err != nil {
		return fmt.Errorf("invalid baseline JSON: %w", err)
	}
	right, err := decodeJSON(b)
	if err != nil {
		return fmt.Errorf("invalid candidate JSON: %w", err)
	}
	if !reflect.DeepEqual(left, right) {
		return fmt.Errorf("JSON differs (including array order, numeric value, null or omitted fields)")
	}
	return nil
}
func CompareSnapshots(a, b Snapshot, comparison string) error {
	if a.CaseID != b.CaseID {
		return fmt.Errorf("case ID differs")
	}
	if a.Status != b.Status {
		return fmt.Errorf("HTTP status differs: baseline=%d candidate=%d", a.Status, b.Status)
	}
	if !reflect.DeepEqual(a.Headers, b.Headers) {
		return fmt.Errorf("contract response headers differ")
	}
	if comparison == "json" {
		return CompareJSON(a.Body, b.Body)
	}
	if comparison != "bytes" {
		return fmt.Errorf("unknown comparison mode")
	}
	if !bytes.Equal(a.Body, b.Body) {
		return fmt.Errorf("response bytes differ")
	}
	return nil
}
