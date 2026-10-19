package protocol

import (
	"context"
	"strings"
	"testing"
)

func BenchmarkNativeRecords32MiB(b *testing.B) {
	data := strings.Repeat(`{"c":"`+strings.Repeat("x", 4096)+`","f":1,"l":123}`+"\n", 8192)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ParseNativeRecords(ctx, strings.NewReader(data), 1<<20, 10000, func(NativeRecord) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}
