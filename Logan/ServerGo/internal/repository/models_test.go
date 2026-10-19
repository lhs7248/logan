package repository

import (
	"gorm.io/gorm/schema"
	"sync"
	"testing"
)

func TestLegacyTableMappings(t *testing.T) {
	for _, c := range []struct {
		model any
		table string
		cols  []string
	}{
		{&NativeTask{}, "logan_task", []string{"id", "platform", "amount", "app_id", "union_id", "device_id", "app_version", "build_version", "log_file_name", "log_date", "add_time", "status", "update_time"}},
		{&NativeDetail{}, "logan_log_detail", []string{"id", "task_id", "log_type", "content", "log_time", "add_time", "update_time"}},
		{&WebTask{}, "web_task", []string{"id", "device_id", "web_source", "environment", "page_num", "content", "add_time", "log_date", "status", "custom_report_info", "update_time"}},
		{&WebDetail{}, "web_detail", []string{"id", "task_id", "log_type", "content", "log_time", "log_level", "add_time", "minute_offset"}},
	} {
		s, err := schema.Parse(c.model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		if s.Table != c.table {
			t.Fatalf("table=%s", s.Table)
		}
		if len(s.DBNames) != len(c.cols) {
			t.Fatalf("%s unexpected columns: %v", c.table, s.DBNames)
		}
		for _, col := range c.cols {
			if s.FieldsByDBName[col] == nil {
				t.Errorf("%s missing %s", c.table, col)
			}
		}
		for _, f := range s.Fields {
			if f.AutoCreateTime != 0 || f.AutoUpdateTime != 0 {
				t.Errorf("implicit timestamp mutation %s.%s", c.table, f.Name)
			}
		}
	}
}
