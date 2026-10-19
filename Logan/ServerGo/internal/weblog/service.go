// Package weblog implements the legacy Web upload and query semantics.
package weblog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"logan/servergo/biz/model"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

var ErrInvalid = errors.New("invalid params")
var ErrSave = errors.New("save log error")
var ErrAnalysis = errors.New("web analysis unavailable")

type Config struct {
	MaxBytes          int64
	MaxTasks, Workers int
	Timeout, Wait     time.Duration
	Timezone          string
}
type Upload struct {
	DeviceID    string  `json:"deviceId"`
	LogPageNo   int32   `json:"logPageNo"`
	WebSource   *string `json:"webSource"`
	Environment *string `json:"environment"`
	LogArray    string  `json:"logArray"`
	FileDate    string  `json:"fileDate"`
	Client      *string `json:"client"`
	CustomInfo  *string `json:"customInfo"`
}
type ParseTask = func(context.Context, repository.WebTask) ([]repository.WebDetail, error)
type Repository interface {
	Save(context.Context, *repository.WebTask) error
	Tasks(context.Context, []int64) ([]repository.WebTask, error)
	Search(context.Context, int64, int64, string) ([]repository.WebTask, error)
	Latest(context.Context) ([]repository.WebTask, error)
	BoundedDetails(context.Context, []int64, int64) ([]repository.WebDetail, error)
	Contents(context.Context, []int64, int64) ([]byte, error)
	MatchIndexes(context.Context, int64, int, int, []int, string, int) ([]repository.WebDetail, error)
	Ensure(context.Context, int64) error
	Pending(context.Context, int) ([]int64, error)
	Process(context.Context, int64, ParseTask) error
	Ready(context.Context, []int64) (bool, error)
	Validate(context.Context) error
}
type Service struct {
	lifecycleMu sync.Mutex
	done        chan struct{}
	stopClaims  chan struct{}
	stopOnce    sync.Once
	started     bool
	observer    func(string, string, time.Duration)
	cfg         Config
	decoder     *protocol.WebDecoder
	repo        Repository
	loc         *time.Location
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	start       sync.Once
	wake        chan struct{}
	slots       chan struct{}
}

func NewService(c Config, d *protocol.WebDecoder, r Repository) (*Service, error) {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil || c.MaxBytes < 1 || c.MaxTasks < 1 || c.Workers < 1 || c.Timeout <= 0 || c.Wait <= 0 || d == nil || r == nil {
		return nil, fmt.Errorf("invalid web service configuration")
	}
	return &Service{done: make(chan struct{}), stopClaims: make(chan struct{}), cfg: c, decoder: d, repo: r, loc: loc, wake: make(chan struct{}, 1), slots: make(chan struct{}, c.Workers*8)}, nil
}
func (s *Service) Start(parent context.Context) {
	s.start.Do(func() {
		s.lifecycleMu.Lock()
		defer s.lifecycleMu.Unlock()
		s.started = true
		ctx, cancel := context.WithCancel(parent)
		s.cancel = cancel
		for i := 0; i < s.cfg.Workers; i++ {
			s.wg.Add(1)
			go s.work(ctx)
		}
		go func() { s.wg.Wait(); close(s.done) }()
	})
}

// SetJobObserver installs a bounded attempt observer; configure before Start.
func (s *Service) SetJobObserver(f func(kind, outcome string, duration time.Duration)) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.observer = f
}

// StopClaims wakes idle workers and allows their accepted task to finish.
func (s *Service) StopClaims() { s.stopOnce.Do(func() { close(s.stopClaims) }) }
func (s *Service) Cancel() {
	s.lifecycleMu.Lock()
	cancel := s.cancel
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *Service) Wait(ctx context.Context) error {
	s.lifecycleMu.Lock()
	started := s.started
	done := s.done
	s.lifecycleMu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Close() { s.Cancel(); _ = s.Wait(context.Background()) }
func (s *Service) claimsStopped() bool {
	select {
	case <-s.stopClaims:
		return true
	default:
		return false
	}
}
func (s *Service) observe(err error, started time.Time) {
	outcome := "success"
	if err != nil {
		outcome = "error"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
	}
	s.lifecycleMu.Lock()
	f := s.observer
	s.lifecycleMu.Unlock()
	if f != nil {
		f("web", outcome, time.Since(started))
	}
}
func (s *Service) work(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.claimsStopped() {
			return
		}
		select {
		case <-s.stopClaims:
			return
		case <-ctx.Done():
			return
		default:
		}
		ids, err := s.repo.Pending(ctx, s.cfg.Workers*2)
		if err != nil {
			slog.Warn("Web processing scan failed", "error_class", repository.ErrorClass(err))
			select {
			case <-s.stopClaims:
				return
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if err == nil {
			for _, id := range ids {
				if ctx.Err() != nil || s.claimsStopped() {
					return
				}
				started := time.Now()
				taskCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
				err := s.repo.Process(taskCtx, id, s.parse)
				s.observe(err, started)
				if err != nil {
					slog.Warn("Web task processing failed", "task_id", id, "error_class", repository.ErrorClass(err))
				}
				cancel()
			}
		}
		select {
		case <-s.stopClaims:
			return
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
func (s *Service) parse(ctx context.Context, t repository.WebTask) ([]repository.WebDetail, error) {
	parsed, err := s.decoder.ParseDetails(ctx, t.Content, t.ID, s.loc)
	if err != nil {
		return nil, err
	}
	out := make([]repository.WebDetail, 0, len(parsed))
	now := time.Now().UnixMilli()
	for _, d := range parsed {
		zero := 0
		out = append(out, repository.WebDetail{TaskID: t.ID, LogType: d.LogType, Content: d.Content, LogTime: d.LogTime, MinuteOffset: d.MinuteOffset, LogLevel: &zero, AddTime: now})
	}
	return out, nil
}

var javaDate = regexp.MustCompile(`^\s*([0-9]+)-([0-9]+)-([0-9]+)`)

func (s *Service) Upload(ctx context.Context, u Upload) error {
	m := javaDate.FindStringSubmatch(u.FileDate)
	if u.DeviceID == "" || u.LogPageNo <= 0 || u.LogArray == "" || m == nil {
		return ErrInvalid
	}
	n := [3]int{}
	for i := range n {
		v, err := strconv.ParseInt(m[i+1], 10, 32)
		if err != nil {
			return ErrInvalid
		}
		n[i] = int(v)
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	content, err := s.decoder.Parse(ctx, u.LogArray)
	if err != nil {
		return err
	}
	task := &repository.WebTask{DeviceID: u.DeviceID, PageNum: int(u.LogPageNo), WebSource: u.WebSource, Environment: u.Environment, CustomReportInfo: u.CustomInfo, Content: content, LogDate: time.Date(n[0], time.Month(n[1]), n[2], 0, 0, 0, 0, s.loc).UnixMilli(), AddTime: time.Now().UnixMilli()}
	if err := s.repo.Save(ctx, task); err != nil {
		slog.Warn("Web upload persistence failed", "error_class", repository.ErrorClass(err))
		return ErrSave
	}
	return nil
}
func (s *Service) check(ids []int64) error {
	if len(ids) > s.cfg.MaxTasks {
		return ErrAnalysis
	}
	return nil
}
func taskDTO(t repository.WebTask) model.WebTask {
	device := t.DeviceID
	var updated *int64
	if !t.UpdateTime.IsZero() {
		v := t.UpdateTime.UnixMilli()
		updated = &v
	}
	return model.WebTask{TaskID: t.ID, DeviceID: &device, WebSource: t.WebSource, Environment: t.Environment, PageNum: t.PageNum, AddTime: t.AddTime, LogDate: t.LogDate, Status: t.Status, CustomReportInfo: t.CustomReportInfo, UpdateTime: updated}
}
func detailDTO(d repository.WebDetail) *model.WebDetail {
	c := d.Content
	level := 0
	if d.LogLevel != nil {
		level = *d.LogLevel
	}
	return &model.WebDetail{ID: d.ID, TaskID: d.TaskID, LogType: d.LogType, Content: &c, LogTime: d.LogTime, LogLevel: level, MinuteOffset: d.MinuteOffset, AddTime: d.AddTime}
}
func (s *Service) Tasks(ctx context.Context, ids []int64) ([]repository.WebTask, error) {
	if err := s.check(ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []repository.WebTask{}, nil
	}
	return s.repo.Tasks(ctx, ids)
}
func (s *Service) Task(ctx context.Context, ids []int64) (*model.WebTask, error) {
	tasks, err := s.Tasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	sort.SliceStable(tasks, func(i, j int) bool { return int32(tasks[i].PageNum-tasks[j].PageNum) < 0 })
	v := taskDTO(tasks[0])
	return &v, nil
}
func group(tasks []repository.WebTask, byDevice bool) []model.WebTask {
	groups := map[string][]repository.WebTask{}
	keys := []string{}
	for _, t := range tasks {
		key := strconv.FormatInt(t.LogDate, 10)
		if byDevice {
			key += "\x00" + t.DeviceID
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], t)
	}
	// ArrayListMultimap uses the JDK HashMap bucket order for keySet().
	dateKeys := map[int64]bool{}
	dateRanks := map[int64]int{}
	devices := map[int64]int{}
	for _, key := range keys {
		date := groups[key][0].LogDate
		if !dateKeys[date] {
			dateRanks[date] = len(dateRanks)
		}
		dateKeys[date] = true
		devices[groups[key][0].LogDate]++
	}
	dateCapacity := javaMapCapacity(len(dateKeys))
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := groups[keys[i]][0], groups[keys[j]][0]
		ab, bb := javaLongBucket(a.LogDate, dateCapacity), javaLongBucket(b.LogDate, dateCapacity)
		if ab != bb {
			return ab < bb
		}
		if a.LogDate != b.LogDate {
			return dateRanks[a.LogDate] < dateRanks[b.LogDate]
		}
		if byDevice {
			cap := javaMapCapacity(devices[a.LogDate])
			return javaStringBucket(a.DeviceID, cap) < javaStringBucket(b.DeviceID, cap)
		}
		return false
	})
	out := make([]model.WebTask, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		sort.SliceStable(g, func(i, j int) bool { return int32(g[i].PageNum-g[j].PageNum) < 0 })
		ids := make([]string, len(g))
		for i, t := range g {
			ids[i] = strconv.FormatInt(t.ID, 10)
		}
		v := taskDTO(g[0])
		joined := strings.Join(ids, ",")
		v.Tasks = &joined
		out = append(out, v)
	}
	return out
}
func (s *Service) Search(ctx context.Context, b, e int64, device string) ([]model.WebTask, error) {
	rows, err := s.repo.Search(ctx, b, e+86400000, device)
	if errors.Is(err, repository.ErrWebLimit) {
		return nil, ErrAnalysis
	}
	if err != nil {
		return []model.WebTask{}, nil
	}
	return group(rows, false), nil
}
func (s *Service) Latest(ctx context.Context) ([]model.WebTask, error) {
	rows, err := s.repo.Latest(ctx)
	if err != nil {
		return []model.WebTask{}, nil
	}
	out := group(rows, true)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LogDate == out[j].LogDate {
			return out[i].AddTime > out[j].AddTime
		}
		return out[i].LogDate > out[j].LogDate
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}
func (s *Service) Details(ctx context.Context, ids []int64) ([]*model.WebDetail, error) {
	if err := s.check(ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.repo.BoundedDetails(ctx, ids, s.cfg.MaxBytes)
	if errors.Is(err, repository.ErrWebLimit) {
		return nil, ErrAnalysis
	}
	if err != nil || len(rows) == 0 {
		return nil, nil
	}
	out := make([]*model.WebDetail, len(ids))
	for _, d := range rows {
		for i, id := range ids {
			if id == d.ID {
				if out[i] == nil {
					out[i] = detailDTO(d)
				}
				break
			}
		}
	}
	return out, nil
}
func (s *Service) Detail(ctx context.Context, id int64) (*model.WebDetail, error) {
	rows, err := s.repo.BoundedDetails(ctx, []int64{id}, s.cfg.MaxBytes)
	if errors.Is(err, repository.ErrWebLimit) {
		return nil, ErrAnalysis
	}
	if err != nil || len(rows) == 0 {
		return nil, nil
	}
	return detailDTO(rows[0]), nil
}
func (s *Service) Download(ctx context.Context, ids []int64) ([]byte, error) {
	if err := s.check(ids); err != nil {
		return nil, err
	}
	return s.repo.Contents(ctx, ids, s.cfg.MaxBytes)
}
func (s *Service) Index(ctx context.Context, ids []int64, types []int, keyword string, begin, end int) ([][]model.WebIndex, error) {
	if err := s.check(ids); err != nil || len(types) > s.cfg.MaxTasks {
		return nil, ErrAnalysis
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return nil, ErrAnalysis
	}
	tasks, err := s.Tasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, ErrInvalid
	}
	existing := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		existing = append(existing, t.ID)
		if err := s.repo.Ensure(ctx, t.ID); err != nil {
			return nil, ErrAnalysis
		}
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	waitCtx, cancel := context.WithTimeout(ctx, s.cfg.Wait)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := s.repo.Ready(waitCtx, existing)
		if err != nil {
			return nil, ErrAnalysis
		}
		if ready {
			break
		}
		select {
		case <-waitCtx.Done():
			return nil, ErrAnalysis
		case <-ticker.C:
		}
	}
	details := []repository.WebDetail{}
	maxIndexes := max(1, int(s.cfg.MaxBytes/256))
	for _, id := range ids {
		rows, err := s.repo.MatchIndexes(ctx, id, begin, end, types, keyword, maxIndexes-len(details))
		if err != nil {
			return nil, ErrAnalysis
		}
		if len(rows) > maxIndexes-len(details) {
			return nil, ErrAnalysis
		}
		details = append(details, rows...)
	}
	sort.SliceStable(details, func(i, j int) bool { return int32(details[i].LogTime-details[j].LogTime) < 0 })
	pages := make([][]model.WebIndex, 0)
	for _, d := range details {
		if len(pages) == 0 || len(pages[len(pages)-1]) == 20 {
			pages = append(pages, make([]model.WebIndex, 0, 20))
		}
		last := len(pages) - 1
		pages[last] = append(pages[last], model.WebIndex{ID: d.ID, LogType: d.LogType, LogTime: d.LogTime})
	}
	return pages, nil
}

func javaMapCapacity(count int) uint32 {
	capacity := uint32(16)
	for count > int(capacity)*3/4 {
		capacity *= 2
	}
	return capacity
}
func javaLongBucket(v int64, capacity uint32) uint32 {
	n := uint64(v)
	hash := uint32(n ^ (n >> 32))
	return (hash ^ (hash >> 16)) & (capacity - 1)
}
func javaStringBucket(v string, capacity uint32) uint32 {
	var hash uint32
	for _, c := range utf16.Encode([]rune(v)) {
		hash = hash*31 + uint32(c)
	}
	return (hash ^ (hash >> 16)) & (capacity - 1)
}
