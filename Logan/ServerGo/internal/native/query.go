package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"logan/servergo/biz/model"
	"logan/servergo/internal/nativecontent"
	"logan/servergo/internal/processingtime"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"os"
	"sort"
	"sync"
	"time"
)

var ErrProcessing = errors.New("native processing unavailable")

type QueryConfig struct {
	MaxCompressed, MaxPlain, MaxStaged   int64
	MaxLine, MaxRecords, MaxIDs, Workers int
	Timeout, Wait                        time.Duration
	TempDir                              string
}
type QueryRepository interface {
	Validate(context.Context) error
	Find(context.Context, int64) (*repository.NativeTask, error)
	Latest(context.Context) ([]repository.NativeTask, error)
	Search(context.Context, string, int64, int64, int) ([]repository.NativeTask, error)
	BoundedDetails(context.Context, []int64, int64) ([]repository.NativeDetail, error)
	MatchIndexes(context.Context, int64, []int, *string, int) ([]repository.NativeDetail, error)
	Ensure(context.Context, int64) error
	Pending(context.Context, int) ([]int64, error)
	Ready(context.Context, int64) (bool, error)
	Process(context.Context, int64, func(context.Context, repository.NativeTask) (io.ReadCloser, error)) error
}
type ObjectReader interface {
	Get(context.Context, string) (io.ReadCloser, error)
}
type QueryService struct {
	lifecycleMu sync.Mutex
	active      map[int64]struct{}
	done        chan struct{}
	stopClaims  chan struct{}
	stopOnce    sync.Once
	started     bool
	observer    func(string, string, time.Duration)
	cfg         QueryConfig
	store       ObjectReader
	repo        QueryRepository
	start       sync.Once
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	wake        chan struct{}
	waiters     chan struct{}
}

func NewQueryService(c QueryConfig, store ObjectReader, repo QueryRepository) (*QueryService, error) {
	if c.MaxCompressed < 1 || c.MaxCompressed == 1<<63-1 || c.MaxPlain < 1 || c.MaxStaged < 1 || c.MaxLine < 1 || c.MaxRecords < 1 || c.MaxIDs < 1 || c.Workers < 1 || c.Timeout <= 0 || c.Wait <= 0 || store == nil || repo == nil {
		return nil, fmt.Errorf("invalid native query configuration")
	}
	if info, err := os.Stat(c.TempDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("native query temporary directory must exist")
	}
	return &QueryService{done: make(chan struct{}), stopClaims: make(chan struct{}), cfg: c, store: store, repo: repo, active: make(map[int64]struct{}), wake: make(chan struct{}, 1), waiters: make(chan struct{}, c.Workers*8)}, nil
}
func (q *QueryService) Start(parent context.Context) {
	q.start.Do(func() {
		q.lifecycleMu.Lock()
		defer q.lifecycleMu.Unlock()
		q.started = true
		ctx, cancel := context.WithCancel(parent)
		q.cancel = cancel
		for i := 0; i < q.cfg.Workers; i++ {
			q.wg.Add(1)
			go q.work(ctx)
		}
		go func() { q.wg.Wait(); close(q.done) }()
	})
}

// SetJobObserver installs a bounded attempt observer; configure before Start.
func (q *QueryService) SetJobObserver(f func(kind, outcome string, duration time.Duration)) {
	q.lifecycleMu.Lock()
	defer q.lifecycleMu.Unlock()
	q.observer = f
}

// StopClaims wakes idle workers and allows their accepted task to finish.
func (q *QueryService) StopClaims() { q.stopOnce.Do(func() { close(q.stopClaims) }) }
func (q *QueryService) Cancel() {
	q.lifecycleMu.Lock()
	cancel := q.cancel
	q.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (q *QueryService) Wait(ctx context.Context) error {
	q.lifecycleMu.Lock()
	started := q.started
	done := q.done
	q.lifecycleMu.Unlock()
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
func (q *QueryService) Close() { q.Cancel(); _ = q.Wait(context.Background()) }
func (q *QueryService) claimsStopped() bool {
	select {
	case <-q.stopClaims:
		return true
	default:
		return false
	}
}
func (q *QueryService) observe(err error, started time.Time) {
	outcome := "success"
	if err != nil {
		outcome = "error"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
	}
	q.lifecycleMu.Lock()
	f := q.observer
	q.lifecycleMu.Unlock()
	if f != nil {
		f("native", outcome, time.Since(started))
	}
}
func (q *QueryService) work(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if q.claimsStopped() {
			return
		}
		if ctx.Err() != nil {
			return
		}
		ids, err := q.repo.Pending(ctx, q.cfg.Workers*2)
		if err != nil {
			slog.Warn("native processing scan failed", "error_class", repository.ErrorClass(err))
			select {
			case <-q.stopClaims:
				return
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for _, id := range ids {
			if ctx.Err() != nil || q.claimsStopped() {
				return
			}
			if !q.reserveTask(id) {
				continue
			}
			func() {
				defer q.releaseTask(id)
				started := time.Now()
				taskCtx, cancel := context.WithTimeout(ctx, q.cfg.Timeout)
				defer cancel()
				err := q.repo.Process(taskCtx, id, q.parse)
				q.observe(err, started)
				if err != nil {
					slog.Warn("native processing failed", "task_id", id, "error_class", repository.ErrorClass(err))
				}
			}()
		}
		select {
		case <-q.stopClaims:
			return
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}
	}
}

// Local reservations avoid redundant SQL claims waiting behind a publication
// row lock. Persistent leases and fences remain authoritative across instances.
// The map has at most Workers entries and every attempt releases its reservation.
func (q *QueryService) reserveTask(id int64) bool {
	q.lifecycleMu.Lock()
	defer q.lifecycleMu.Unlock()
	if _, ok := q.active[id]; ok {
		return false
	}
	q.active[id] = struct{}{}
	return true
}
func (q *QueryService) releaseTask(id int64) {
	q.lifecycleMu.Lock()
	delete(q.active, id)
	q.lifecycleMu.Unlock()
}
func nativeTaskDTO(t repository.NativeTask) model.NativeTask {
	var date int64
	if t.LogDate != nil {
		date = *t.LogDate
	}
	return model.NativeTask{TaskID: t.ID, Amount: t.Amount, AppID: t.AppID, UnionID: t.UnionID, Platform: t.Platform, BuildVersion: t.BuildVersion, AppVersion: t.AppVersion, DeviceID: t.DeviceID, LogDate: date, LogFileName: t.LogFileName, AddTime: t.AddTime, Status: t.Status}
}
func nativeTasksDTO(rows []repository.NativeTask) []model.NativeTask {
	out := make([]model.NativeTask, 0, len(rows))
	for _, r := range rows {
		out = append(out, nativeTaskDTO(r))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LogDate == out[j].LogDate {
			return out[i].TaskID > out[j].TaskID
		}
		return out[i].LogDate > out[j].LogDate
	})
	return out
}
func (q *QueryService) Latest(ctx context.Context) ([]model.NativeTask, error) {
	rows, err := q.repo.Latest(ctx)
	if err != nil {
		if errors.Is(err, repository.ErrWebLimit) {
			return nil, ErrProcessing
		}
		return []model.NativeTask{}, nil
	}
	return nativeTasksDTO(rows), nil
}
func (q *QueryService) Search(ctx context.Context, device string, begin, end int64, platform int) ([]model.NativeTask, error) {
	rows, err := q.repo.Search(ctx, device, begin, end, platform)
	if err != nil {
		if errors.Is(err, repository.ErrWebLimit) {
			return nil, ErrProcessing
		}
		return []model.NativeTask{}, nil
	}
	return nativeTasksDTO(rows), nil
}
func (q *QueryService) Info(ctx context.Context, id int64) (*model.NativeTask, error) {
	row, err := q.repo.Find(ctx, id)
	if err != nil || row == nil {
		return nil, nil
	}
	dto := nativeTaskDTO(*row)
	return &dto, nil
}
func (q *QueryService) Brief(ctx context.Context, id int64, types []int, keyword *string) ([][]model.NativeSimple, error) {
	if len(types) > q.cfg.MaxIDs {
		return nil, ErrProcessing
	}
	select {
	case q.waiters <- struct{}{}:
		defer func() { <-q.waiters }()
	default:
		return nil, ErrProcessing
	}
	task, err := q.repo.Find(ctx, id)
	if err != nil || task == nil {
		return [][]model.NativeSimple{}, nil
	}
	if task.Status == 0 {
		if err := q.repo.Ensure(ctx, id); err != nil {
			return nil, ErrProcessing
		}
		select {
		case q.wake <- struct{}{}:
		default:
		}
		waitCtx, cancel := context.WithTimeout(ctx, q.cfg.Wait)
		defer cancel()
		// Poll persisted readiness at a bounded rate; another instance may own
		// the task, so local wakeups cannot replace the database check.
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			ready, err := q.repo.Ready(waitCtx, id)
			if err != nil {
				return nil, ErrProcessing
			}
			if ready {
				break
			}
			select {
			case <-waitCtx.Done():
				return nil, ErrProcessing
			case <-ticker.C:
			}
		}
	}
	rows, err := q.repo.MatchIndexes(ctx, id, types, keyword, max(1, int(q.cfg.MaxPlain/256)))
	if err != nil {
		return nil, ErrProcessing
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	out := make([][]model.NativeSimple, 0)
	for _, r := range rows {
		if len(out) == 0 || len(out[len(out)-1]) == 20 {
			out = append(out, make([]model.NativeSimple, 0, 20))
		}
		last := len(out) - 1
		out[last] = append(out[last], model.NativeSimple{ID: r.ID, LogType: r.LogType, LogTime: r.LogTime})
	}
	return out, nil
}
func (q *QueryService) Details(ctx context.Context, ids []int64) ([]*model.NativeDetail, error) {
	if len(ids) > q.cfg.MaxIDs {
		return nil, ErrProcessing
	}
	rows, err := q.repo.BoundedDetails(ctx, ids, q.cfg.MaxPlain)
	if err != nil {
		if errors.Is(err, repository.ErrWebLimit) {
			return nil, ErrProcessing
		}
		return []*model.NativeDetail{}, nil
	}
	if len(rows) == 0 {
		return []*model.NativeDetail{}, nil
	}
	out := make([]*model.NativeDetail, len(ids))
	for _, r := range rows {
		for i, id := range ids {
			if r.ID == id {
				if out[i] == nil {
					simple, err := nativecontent.Simple(r.LogType, r.Content)
					if err != nil {
						return []*model.NativeDetail{}, nil
					}
					content := r.Content
					out[i] = &model.NativeDetail{ID: r.ID, TaskID: r.TaskID, LogType: r.LogType, Content: &content, SimpleContent: &simple, LogTime: r.LogTime}
				}
				break
			}
		}
	}
	return out, nil
}
func (q *QueryService) Detail(ctx context.Context, id int64) (*model.NativeDetail, error) {
	rows, err := q.repo.BoundedDetails(ctx, []int64{id}, q.cfg.MaxPlain)
	if err != nil {
		if errors.Is(err, repository.ErrWebLimit) {
			return nil, ErrProcessing
		}
		return nil, nil
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	formatted, err := nativecontent.Format(r.LogType, r.Content)
	if err != nil {
		return nil, nil
	}
	content := r.Content
	return &model.NativeDetail{ID: r.ID, TaskID: r.TaskID, LogType: r.LogType, Content: &content, FormatContent: &formatted, LogTime: r.LogTime}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type stageWriter struct {
	w    io.Writer
	left int64
}

func (w *stageWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.left {
		return 0, protocol.ErrLimit
	}
	n, err := w.w.Write(b)
	w.left -= int64(n)
	return n, err
}

type stagedNative struct {
	*os.File
	once sync.Once
	err  error
}

func (f *stagedNative) Close() error {
	f.once.Do(func() { f.err = errors.Join(f.File.Close(), os.Remove(f.Name())) })
	return f.err
}
func removeNativeTemp(f *os.File) {
	_ = f.Close()
	if err := os.Remove(f.Name()); err != nil {
		slog.Warn("native parse temporary file cleanup failed")
	}
}
func (q *QueryService) parse(ctx context.Context, task repository.NativeTask) (io.ReadCloser, error) {
	if task.LogFileName == nil || *task.LogFileName == "" {
		return nil, fmt.Errorf("native source missing")
	}
	downloadStarted := time.Now()
	body, err := q.store.Get(ctx, *task.LogFileName)
	if err != nil {
		return nil, err
	}
	var closeOnce sync.Once
	closeBody := func() { closeOnce.Do(func() { _ = body.Close() }) }
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeBody(); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
		closeBody()
	}()
	compressed, err := os.CreateTemp(q.cfg.TempDir, "logan-native-download-*.gz")
	if err != nil {
		return nil, err
	}
	defer removeNativeTemp(compressed)
	size, err := io.CopyBuffer(compressed, io.LimitReader(contextReader{ctx, body}, q.cfg.MaxCompressed+1), make([]byte, 32<<10))
	if err != nil {
		return nil, err
	}
	processingtime.Add(ctx, "download", time.Since(downloadStarted))
	if size > q.cfg.MaxCompressed {
		return nil, protocol.ErrLimit
	}
	if _, err := compressed.Seek(0, 0); err != nil {
		return nil, err
	}
	plain, err := os.CreateTemp(q.cfg.TempDir, "logan-native-plain-*")
	if err != nil {
		return nil, err
	}
	defer removeNativeTemp(plain)
	decompressStarted := time.Now()
	decompressErr := protocol.Decompress(ctx, compressed, plain, q.cfg.MaxPlain)
	processingtime.Add(ctx, "decompress", time.Since(decompressStarted))
	if decompressErr != nil {
		return nil, decompressErr
	}
	if _, err := plain.Seek(0, 0); err != nil {
		return nil, err
	}
	stage, err := os.CreateTemp(q.cfg.TempDir, "logan-native-stage-*")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			removeNativeTemp(stage)
		}
	}()
	encoder := json.NewEncoder(&stageWriter{stage, q.cfg.MaxStaged})
	encoder.SetEscapeHTML(false)
	parseStarted := time.Now()
	err = protocol.ParseNativeRecords(ctx, plain, q.cfg.MaxLine, q.cfg.MaxRecords, func(r protocol.NativeRecord) error {
		if r.Content == nil {
			return fmt.Errorf("native record content cannot be null")
		}
		return encoder.Encode(repository.NativeDetail{TaskID: task.ID, LogType: r.LogType, Content: *r.Content, LogTime: r.LogTime})
	})
	processingtime.Add(ctx, "parse_stage", time.Since(parseStarted))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := stage.Seek(0, 0); err != nil {
		return nil, err
	}
	success = true
	return &stagedNative{File: stage}, nil
}
