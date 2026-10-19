package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestNativeRecordJavaVectors(t *testing.T) {
	b, e := os.ReadFile("../../testdata/protocol/java-native-records.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Records []struct {
			Input   string
			Records []NativeRecord
			Error   bool
		}
	}
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, v := range fixture.Records {
		var got []NativeRecord
		e = ParseNativeRecords(context.Background(), strings.NewReader(v.Input), 1<<20, 100, func(r NativeRecord) error { got = append(got, r); return nil })
		if (e != nil) != v.Error {
			t.Errorf("%q error=%v want %v", v.Input, e, v.Error)
		}
		a, _ := json.Marshal(got)
		want, _ := json.Marshal(v.Records)
		if string(a) == "null" {
			a = []byte("[]")
		}
		if string(a) != string(want) {
			t.Errorf("%q got %s want %s", v.Input, a, want)
		}
	}
}
func TestNativeRecordBudgets(t *testing.T) {
	s := "{\"c\":\"" + strings.Repeat("x", 100000) + "\"}"
	if e := ParseNativeRecords(context.Background(), strings.NewReader(s), len(s), 1, func(NativeRecord) error { return nil }); e != nil {
		t.Fatal(e)
	}
	for _, input := range []string{s, "{}\r{}"} {
		if e := ParseNativeRecords(context.Background(), strings.NewReader(input), 100, 1, func(NativeRecord) error { return nil }); !errors.Is(e, ErrLimit) {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := ParseNativeRecords(ctx, strings.NewReader("{}"), 100, 1, func(NativeRecord) error { return nil }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestNativeRecordStreamingErrors(t *testing.T) {
	sentinel := errors.New("emit failure")
	if e := ParseNativeRecords(context.Background(), strings.NewReader("{}\n{}"), 100, 2, func(NativeRecord) error { return sentinel }); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	if e := ParseNativeRecords(ctx, strings.NewReader("{}\r\n{}"), 100, 2, func(NativeRecord) error { count++; cancel(); return nil }); !errors.Is(e, context.Canceled) || count != 1 {
		t.Fatal(e, count)
	}
	for _, s := range []string{"\u00a0", "\u2007", "\u202f", "{'c':'x'}", "{c:1}", "{} garbage"} {
		if e := ParseNativeRecords(context.Background(), strings.NewReader(s), 100, 2, func(NativeRecord) error { return nil }); e == nil {
			t.Fatalf("accepted unsupported or nonblank input %q", s)
		}
	}
	for _, s := range []string{"", "\r\n\r\n", " \t\v\f\u001c\u2003\u2028\u2029"} {
		if e := ParseNativeRecords(context.Background(), strings.NewReader(s), 100, 1, func(NativeRecord) error { t.Fatal("emitted blank"); return nil }); e != nil {
			t.Fatal(e)
		}
	}
}

// checkpointContext cancels at a deterministic conversion checkpoint, so the
// test exercises cancellation during conversion without scheduling or timing.
type checkpointContext struct {
	context.Context
	calls, cancelAt int
}

func (c *checkpointContext) Err() error {
	c.calls++
	if c.cancelAt > 0 && c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func TestNativeCompositeLargeObjectCancellation(t *testing.T) {
	const count = 10000
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"key`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":[1e2,null,{"drop":null,"keep":-0}]`)
	}
	b.WriteByte('}')
	raw := json.RawMessage(b.String())
	complete := &checkpointContext{Context: context.Background()}
	out, e := nativeComposite(complete, raw, 0)
	if e != nil {
		t.Fatal(e)
	}
	var got map[string]json.RawMessage
	if e = json.Unmarshal([]byte(out), &got); e != nil || len(got) != count {
		t.Fatal(e, len(got))
	}
	if string(got["key9999"]) != `[1E+2,null,{"keep":0}]` {
		t.Fatal(string(got["key9999"]))
	}
	// Cancel both in the property/ordering phase and near the end of recursive
	// serialization. Neither case depends on a goroutine winning a race.
	for _, at := range []int{count / 2, complete.calls / 2, complete.calls - 3} {
		ctx := &checkpointContext{Context: context.Background(), cancelAt: at}
		if _, e = nativeComposite(ctx, raw, 0); !errors.Is(e, context.Canceled) {
			t.Fatalf("checkpoint %d/%d: %v", at, complete.calls, e)
		}
		if ctx.calls != at {
			t.Fatalf("conversion ignored cancellation: calls %d cutoff %d", ctx.calls, at)
		}
	}
}

func FuzzNativeRecords(f *testing.F) {
	for _, raw := range []string{`{"c":"hello","f":1,"l":1}`, `{"C":{"x":null},"F":2,"L":42}`, "{}\r\n{}\n", "{bad}", `{"c":[1e2,-0,{"b":null}]}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 16384 {
			t.Skip()
		}
		count := 0
		_ = ParseNativeRecords(context.Background(), strings.NewReader(raw), 8192, 100, func(r NativeRecord) error {
			count++
			if r.LogType < 0 || r.LogType > 3 || count > 100 {
				t.Fatal("record bounds or type fallback failed")
			}
			return nil
		})
	})
}

// Checking cancellation once per byte turns a large record into millions of
// context calls. Keep checks frequent but bounded by input blocks and records.
func TestNativeLargeRecordCancellationChecksHaveBlockBudget(t *testing.T) {
	raw := `{"c":"` + strings.Repeat("x", 1<<20) + `","f":1}` + "\r\n"
	ctx := &checkpointContext{Context: context.Background()}
	count := 0
	if err := ParseNativeRecords(ctx, strings.NewReader(raw), 2<<20, 1, func(NativeRecord) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 1 || ctx.calls > 4096 {
		t.Fatalf("large-record work amplified: records=%d cancellation checks=%d", count, ctx.calls)
	}
}

func TestNativeCRLFAcrossReadFragmentBoundary(t *testing.T) {
	first := `{"c":"` + strings.Repeat("x", 4087) + `"}`
	if len(first) != 4095 {
		t.Fatal(len(first))
	}
	for _, sep := range []string{"\r\n", "\r"} {
		input := first + sep + `{"c":"second"}` + "\r" + `{"c":"third"}`
		var got []NativeRecord
		if err := ParseNativeRecords(context.Background(), strings.NewReader(input), 4095, 3, func(r NativeRecord) error { got = append(got, r); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || *got[1].Content != "second" || *got[2].Content != "third" {
			t.Fatal(got)
		}
	}
}
