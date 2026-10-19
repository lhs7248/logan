package handler

import (
	"context"
	"github.com/cloudwego/hertz/pkg/app"
	"logan/servergo/biz/model"
	"logan/servergo/internal/nativecontent"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type NativeQueryService interface {
	Latest(context.Context) ([]model.NativeTask, error)
	Search(context.Context, string, int64, int64, int) ([]model.NativeTask, error)
	Info(context.Context, int64) (*model.NativeTask, error)
	Brief(context.Context, int64, []int, *string) ([][]model.NativeSimple, error)
	Details(context.Context, []int64) ([]*model.NativeDetail, error)
	Detail(context.Context, int64) (*model.NativeDetail, error)
}
type NativeQueries struct {
	service NativeQueryService
	timeout time.Duration
}

func NewNativeQueries(s NativeQueryService, timeout time.Duration) *NativeQueries {
	return &NativeQueries{s, timeout}
}
func (n *NativeQueries) CORS(ctx context.Context, c *app.RequestContext) {
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	(&Native{}).CORS(ctx, c)
}
func javaWhitespace(r rune) bool {
	return r != '\u00a0' && r != '\u2007' && r != '\u202f' && (unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f)
}
func springInteger(s string, bits int) (int64, error) {
	text := strings.Map(func(r rune) rune {
		if javaWhitespace(r) {
			return -1
		}
		return r
	}, s)
	prefix := 0
	if strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") {
		prefix = 1
	}
	base := 10
	if len(text) >= prefix+2 && (strings.HasPrefix(text[prefix:], "0x") || strings.HasPrefix(text[prefix:], "0X")) {
		base = 16
		text = text[:prefix] + text[prefix+2:]
	} else if len(text) > prefix && text[prefix] == '#' {
		base = 16
		text = text[:prefix] + text[prefix+1:]
	}
	return strconv.ParseInt(text, base, bits)
}
func optionalNumber(c *app.RequestContext, name string, bits int) (*int64, error) {
	raw := c.Query(name)
	if strings.TrimFunc(raw, javaWhitespace) == "" {
		return nil, nil
	}
	v, err := springInteger(raw, bits)
	return &v, err
}
func nativeLongList(raw string) []int64 {
	out := []int64{}
	if raw == "" {
		return out
	}
	trimmed := strings.TrimRight(raw, ",")
	if trimmed == "" {
		return out
	}
	for _, s := range strings.Split(trimmed, ",") {
		out = append(out, javaLong(s))
	}
	return out
}
func (n *NativeQueries) Latest(ctx context.Context, c *app.RequestContext) {
	rows, err := n.service.Latest(ctx)
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(rows))
}
func (n *NativeQueries) Search(ctx context.Context, c *app.RequestContext) {
	begin, err := optionalNumber(c, "beginTime", 64)
	end, err2 := optionalNumber(c, "endTime", 64)
	platform, err3 := optionalNumber(c, "platform", 32)
	if err != nil || err2 != nil || err3 != nil {
		empty(c, 400)
		return
	}
	device := springStringQuery(c, "deviceId")
	if device == "" {
		Write(c, 200, Failure(400, "param illegal"))
		return
	}
	if end == nil {
		empty(c, 500)
		return
	}
	adjustedEnd := *end + 86400000
	adjustedBegin := adjustedEnd - 7*86400000
	if begin != nil {
		adjustedBegin = *begin
	}
	p := 0
	if platform != nil {
		p = int(*platform)
	}
	rows, err := n.service.Search(ctx, device, adjustedBegin, adjustedEnd, p)
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(rows))
}
func nativePathID(c *app.RequestContext, name string, bits int) (int64, bool) {
	id, err := springInteger(c.Param(name), bits)
	if err != nil {
		empty(c, 400)
		return 0, false
	}
	if id <= 0 {
		Write(c, 200, Failure(400, "param illegal"))
		return 0, false
	}
	return id, true
}
func (n *NativeQueries) Info(ctx context.Context, c *app.RequestContext) {
	id, ok := nativePathID(c, "taskId", 64)
	if !ok {
		return
	}
	row, err := n.service.Info(ctx, id)
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(row))
}
func (n *NativeQueries) LogTypes(_ context.Context, c *app.RequestContext) {
	Write(c, 200, Success(nativecontent.LogTypes()))
}
func (n *NativeQueries) Brief(ctx context.Context, c *app.RequestContext) {
	id, ok := nativePathID(c, "taskId", 32)
	if !ok {
		return
	}
	var keyword *string
	if c.QueryArgs().Has("keyword") {
		s := springStringQuery(c, "keyword")
		keyword = &s
	}
	rows, err := n.service.Brief(ctx, id, logTypes(springStringQuery(c, "logTypes")), keyword)
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(rows))
}
func (n *NativeQueries) Details(ctx context.Context, c *app.RequestContext) {
	raw := springStringQuery(c, "detailIds")
	if strings.TrimFunc(raw, javaWhitespace) == "" {
		Write(c, 200, Failure(400, "param illegal"))
		return
	}
	rows, err := n.service.Details(ctx, nativeLongList(raw))
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(rows))
}
func (n *NativeQueries) Detail(ctx context.Context, c *app.RequestContext) {
	id, ok := nativePathID(c, "detailId", 32)
	if !ok {
		return
	}
	row, err := n.service.Detail(ctx, id)
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(row))
}

func springStringQuery(c *app.RequestContext, key string) string {
	values := c.QueryArgs().PeekAll(key)
	if len(values) == 1 {
		return string(values[0])
	}
	text := make([]string, len(values))
	for i, v := range values {
		text[i] = string(v)
	}
	return strings.Join(text, ",")
}
