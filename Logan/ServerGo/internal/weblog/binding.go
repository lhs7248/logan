package weblog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// UnmarshalJSON retains Jackson's scalar-to-string and numeric-page coercions.
func (u *Upload) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*u = Upload{}
	text := func(key string) (*string, error) {
		raw, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, nil
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return &value, nil
		}
		var scalar any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&scalar) != nil {
			return nil, fmt.Errorf("invalid %s", key)
		}
		switch v := scalar.(type) {
		case json.Number:
			value = v.String()
		case bool:
			value = strconv.FormatBool(v)
		default:
			return nil, fmt.Errorf("invalid %s", key)
		}
		return &value, nil
	}
	for name, dst := range map[string]*string{"deviceId": &u.DeviceID, "logArray": &u.LogArray, "fileDate": &u.FileDate} {
		v, err := text(name)
		if err != nil {
			return err
		}
		if v != nil {
			*dst = *v
		}
	}
	for name, dst := range map[string]**string{"webSource": &u.WebSource, "environment": &u.Environment, "client": &u.Client, "customInfo": &u.CustomInfo} {
		v, err := text(name)
		if err != nil {
			return err
		}
		*dst = v
	}
	if raw, ok := fields["logPageNo"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var number any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&number) != nil {
			return fmt.Errorf("invalid logPageNo")
		}
		switch v := number.(type) {
		case string:
			v = strings.TrimSpace(v)
			if v == "" {
				return nil
			}
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return fmt.Errorf("invalid logPageNo")
			}
			u.LogPageNo = int32(n)
		case json.Number:
			n, err := strconv.ParseFloat(v.String(), 64)
			if err != nil || n < -2147483648 || n > 2147483647 {
				return fmt.Errorf("invalid logPageNo")
			}
			u.LogPageNo = int32(n)
		default:
			return fmt.Errorf("invalid logPageNo")
		}
	}
	return nil
}
