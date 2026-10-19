package model

import (
	"encoding/json"
	"logan/servergo/internal/contract"
	"os"
	"testing"
)

func TestDTOsMatchJavaModelSerialization(t *testing.T) {
	stamp := int64(1234)
	models := map[string]any{
		"nativeTask":   NativeTask{TaskID: 9007199254740993},
		"nativeDetail": NativeDetail{},
		"nativeSimple": NativeSimple{},
		"webTask":      WebTask{UpdateTime: &stamp},
		"webDetail":    WebDetail{},
	}
	expected, err := os.ReadFile("../../tests/contract/golden/java-models.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.CompareJSON(expected, got); err != nil {
		t.Fatalf("Java DTO serialization mismatch: %v", err)
	}
}
