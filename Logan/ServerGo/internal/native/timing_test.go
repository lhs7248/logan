package native

import (
	"context"
	"io"
	"logan/servergo/internal/processingtime"
	"logan/servergo/internal/repository"
	"testing"
)

func TestParseStageTraceKeepsFailureAndSuccessDurations(t *testing.T) {
	for _, plain := range []string{`{"c":"hello","f":1,"l":2}` + "\n", "{broken}\n"} {
		q, _, _, _ := queryFixture(t, plain)
		ctx, trace := processingtime.New(context.Background())
		name := "test.log"
		stream, err := q.parse(ctx, repository.NativeTask{ID: 1, LogFileName: &name})
		if stream != nil {
			_, _ = io.Copy(io.Discard, stream)
			_ = stream.Close()
		}
		if (err != nil) != (plain == "{broken}\n") {
			t.Fatal(err)
		}
		stages := trace.Milliseconds()
		for _, stage := range []string{"download", "decompress", "parse_stage"} {
			if stages[stage] <= 0 {
				t.Fatalf("%s timing omitted: %v (error=%v)", stage, stages, err)
			}
		}
	}
}
