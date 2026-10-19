package nativecontent

import (
	"encoding/json"
	"os"
	"testing"
)

func TestJavaContentVectors(t *testing.T) {
	b, e := os.ReadFile("../../testdata/protocol/java-native-records.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Handlers []struct {
			Type                    int
			Content, Simple, Format string
		}
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	for _, v := range f.Handlers {
		s, e := Simple(v.Type, v.Content)
		if e != nil || s != v.Simple {
			t.Fatal(v, s, e)
		}
		s, e = Format(v.Type, v.Content)
		if e != nil || s != v.Format {
			t.Fatal(v, s, e)
		}
	}
	if len(LogTypes()) != 3 {
		t.Fatal(LogTypes())
	}
}
