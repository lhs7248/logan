package native

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestMetadataMatchesJavaRequestParser(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/protocol/java-native-metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Headers  map[string]string `json:"headers"`
		Amount   string            `json:"amount"`
		Platform int               `json:"platform"`
		LogDate  int64             `json:"log_date"`
		Prefix   string            `json:"file_name_prefix"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for _, c := range cases {
		task := NewTask(c.Headers, 3, time.UnixMilli(1234), loc, "fixed-uuid")
		if *task.Amount != c.Amount || task.Platform != c.Platform || *task.LogDate != c.LogDate || *task.LogFileName != c.Prefix+"fixed-uuid.log" {
			t.Fatalf("Java metadata differs for %v", c.Headers)
		}
		if task.AddTime != 1234 || task.Status != 0 {
			t.Fatal("incorrect initial task state")
		}
	}
}
