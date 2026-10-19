package handler

import (
	"github.com/cloudwego/hertz/pkg/common/ut"
	"logan/servergo/internal/contract"
	"testing"
)

func TestWriteMatchesRecordedJavaEnvelope(t *testing.T) {
	baseline, err := contract.LoadSnapshot("../../tests/contract/golden/native-info-invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	c := ut.CreateUtRequestContext("GET", "/logan/task/0/info.json", nil)
	Write(c, 200, Failure(400, "param illegal"))
	candidate := contract.Snapshot{CaseID: baseline.CaseID, Status: c.Response.StatusCode(), Headers: map[string]string{"Content-Type": string(c.Response.Header.ContentType())}, Body: c.Response.Body()}
	if err := contract.CompareSnapshots(baseline, candidate, "json"); err != nil {
		t.Fatal(err)
	}
}
