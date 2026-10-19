//go:build integration

package mysql

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"logan/servergo/internal/repository"
)

// Pause the first INSERT after it acquired its lock. The second request must
// actually be waiting in InnoDB before the first continues: no timing guesses.
func concurrentLockUpgrade(t *testing.T, db *gorm.DB, table string, action func(context.Context, int) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var paused int32
	name := "lock-regression-" + table
	must(t, db.Callback().Raw().After("gorm:raw").Register(name, func(tx *gorm.DB) {
		if strings.HasPrefix(tx.Statement.SQL.String(), "INSERT") && strings.Contains(tx.Statement.SQL.String(), table) && atomic.CompareAndSwapInt32(&paused, 0, 1) {
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}))
	defer db.Callback().Raw().Remove(name)
	result := make(chan error, 2)
	go func() { result <- action(ctx, 0) }()
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal("first insert lock barrier not reached")
	}
	go func() { result <- action(ctx, 1) }()
	waiting := false
	for ctx.Err() == nil {
		var count int64
		err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks b ON w.BLOCKING_ENGINE_LOCK_ID=b.ENGINE_LOCK_ID WHERE b.OBJECT_SCHEMA=DATABASE() AND b.OBJECT_NAME=?", table).Scan(&count).Error
		if err != nil {
			t.Fatal("lock observation failed", err)
		}
		if count > 0 {
			waiting = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	once.Do(func() { close(release) })
	if !waiting {
		t.Fatal("second request never reached real lock wait")
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("concurrent operation must succeed without lock upgrade deadlock: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent operation timed out")
		}
	}
}

func TestMySQLWebUploadIdentityLockUpgrade(t *testing.T) {
	db := dbFor(t)
	repo := repository.NewWebRepository(db)
	date := time.Now().UnixMilli()
	seed := repository.WebTask{DeviceID: "lock-seed", LogDate: date, PageNum: 1, Content: "[]"}
	must(t, repo.Save(context.Background(), &seed))
	concurrentLockUpgrade(t, db, "logan_web_upload_lock", func(ctx context.Context, i int) error {
		task := repository.WebTask{DeviceID: fmt.Sprintf("lock-upload-%d", i), LogDate: date, PageNum: 1, Content: "[]"}
		return repo.Save(ctx, &task)
	})
	var n int64
	must(t, db.Model(&repository.WebTask{}).Where("log_date=?", date).Count(&n).Error)
	if n != 3 {
		t.Fatalf("successful upload identities lost or duplicated: %d", n)
	}
}

func TestMySQLEnsureIdentityLockUpgrade(t *testing.T) {
	for _, kind := range []string{"native", "web"} {
		t.Run(kind, func(t *testing.T) {
			db := dbFor(t)
			var ensure func(context.Context, int64) error
			var id int64
			if kind == "native" {
				source := "lock-native"
				task := repository.NativeTask{LogFileName: &source}
				must(t, db.Create(&task).Error)
				id = task.ID
				ensure = repository.NewNativeQueries(db, 1<<20).Ensure
			} else {
				repo := repository.NewWebRepository(db)
				task := repository.WebTask{DeviceID: "ensure-lock", LogDate: time.Now().UnixNano(), PageNum: 1, Content: "[]"}
				must(t, repo.Save(context.Background(), &task))
				id = task.ID
				ensure = repo.Ensure
			}
			must(t, ensure(context.Background(), id))
			concurrentLockUpgrade(t, db, "logan_processing_job", func(ctx context.Context, _ int) error { return ensure(ctx, id) })
			var n int64
			must(t, db.Table("logan_processing_job").Where("kind=? AND task_id=?", kind, id).Count(&n).Error)
			if n != 1 {
				t.Fatal("ensure duplicated job")
			}
		})
	}
}

func TestMySQLPendingDoesNotLockLiveLease(t *testing.T) {
	for _, kind := range []string{"native", "web"} {
		t.Run(kind, func(t *testing.T) {
			db := dbFor(t)
			id := int64(900000001)
			must(t, db.Exec("INSERT INTO logan_processing_job(kind,task_id,state,owner,fence,attempts,lease_until,created_at,updated_at) VALUES (?,?,'running','live-owner',9,1,UNIX_TIMESTAMP()*1000+60000,0,0)", kind, id).Error)
			tx := db.Begin()
			must(t, tx.Error)
			defer tx.Rollback()
			var locked int64
			must(t, tx.Raw("SELECT task_id FROM logan_processing_job WHERE kind=? AND task_id=? FOR UPDATE", kind, id).Scan(&locked).Error)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			var err error
			if kind == "native" {
				_, err = repository.NewNativeQueries(db, 1<<20).Pending(ctx, 10)
			} else {
				_, err = repository.NewWebRepository(db).Pending(ctx, 10)
			}
			if err != nil {
				t.Fatal("scanner blocked on an unexpired owned lease", err)
			}
			var job struct {
				State, Owner string
				Fence        int64
			}
			must(t, db.Raw("SELECT state,owner,fence FROM logan_processing_job WHERE kind=? AND task_id=?", kind, id).Scan(&job).Error)
			if job.State != "running" || job.Owner != "live-owner" || job.Fence != 9 {
				t.Fatal("live ownership changed", job)
			}
		})
	}
}

func TestMySQLExpirationCandidateOwnershipRace(t *testing.T) {
	for _, kind := range []string{"native", "web"} {
		for _, change := range []string{"renew", "owner", "fence", "lease"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				db := dbFor(t)
				id := int64(910000001)
				const oldLease int64 = -1000000000000
				must(t, db.Exec("INSERT INTO logan_processing_job(kind,task_id,state,owner,fence,attempts,lease_until,created_at,updated_at) VALUES (?,?,'running','old-owner',9,3,?,0,0)", kind, id, oldLease).Error)
				defer db.Exec("DELETE FROM logan_processing_job WHERE kind=? AND task_id=?", kind, id)
				var observed int32
				var mutationErr error
				name := "expiration-race"
				must(t, db.Callback().Row().After("gorm:row").Register(name, func(tx *gorm.DB) {
					if strings.HasPrefix(tx.Statement.SQL.String(), "SELECT task_id,owner,fence,lease_until FROM logan_processing_job") && atomic.CompareAndSwapInt32(&observed, 0, 1) {
						update := "owner='new-owner'"
						switch change {
						case "renew":
							update = "lease_until=UNIX_TIMESTAMP()*1000+60000"
						case "fence":
							update = "fence=10"
						case "lease":
							update = "lease_until=lease_until+1"
						}
						mutationErr = db.Exec("UPDATE logan_processing_job SET "+update+" WHERE kind=? AND task_id=?", kind, id).Error
					}
				}))
				defer db.Callback().Row().Remove(name)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var err error
				if kind == "native" {
					_, err = repository.NewNativeQueries(db, 1<<20).Pending(ctx, 1)
				} else {
					_, err = repository.NewWebRepository(db).Pending(ctx, 1)
				}
				must(t, err)
				must(t, mutationErr)
				if atomic.LoadInt32(&observed) != 1 {
					t.Fatal("candidate-read race barrier not exercised")
				}
				var job struct {
					State, Owner      string
					Fence, LeaseUntil int64
				}
				must(t, db.Raw("SELECT state,owner,fence,lease_until FROM logan_processing_job WHERE kind=? AND task_id=?", kind, id).Scan(&job).Error)
				if job.State != "running" {
					t.Fatal("stale scan terminated changed claim", job)
				}
				if change == "owner" && job.Owner != "new-owner" || change == "fence" && job.Fence != 10 || change == "lease" && job.LeaseUntil != oldLease+1 || change == "renew" && job.LeaseUntil <= time.Now().UnixMilli() {
					t.Fatal("replacement or renewal overwritten", job)
				}
			})
		}
	}
}

func TestMySQLExpirationIsBoundedAndTerminal(t *testing.T) {
	for _, kind := range []string{"native", "web"} {
		t.Run(kind, func(t *testing.T) {
			db := dbFor(t)
			for i := 0; i < 3; i++ {
				must(t, db.Exec("INSERT INTO logan_processing_job(kind,task_id,state,owner,fence,attempts,lease_until,created_at,updated_at) VALUES (?,?,'running','expired',11,3,-2000000000000,0,0)", kind, 920000001+i).Error)
			}
			defer db.Exec("DELETE FROM logan_processing_job WHERE kind=? AND task_id BETWEEN 920000001 AND 920000003", kind)
			var err error
			if kind == "native" {
				_, err = repository.NewNativeQueries(db, 1<<20).Pending(context.Background(), 2)
			} else {
				_, err = repository.NewWebRepository(db).Pending(context.Background(), 2)
			}
			must(t, err)
			var count int64
			must(t, db.Table("logan_processing_job").Where("kind=? AND task_id BETWEEN 920000001 AND 920000003 AND state='failed' AND last_error_code='attempts_exhausted' AND owner='expired' AND fence=11 AND attempts=3", kind).Count(&count).Error)
			if count != 2 {
				t.Fatalf("expiration changed %d jobs; want bounded two with ownership metadata intact", count)
			}
		})
	}
}

// Empty task ranges share an index gap. Both publishers reach their insert
// only after cleanup, so a range DELETE makes this interleaving deadlock.
func TestMySQLConcurrentEmptyDetailPublication(t *testing.T) {
	for _, kind := range []string{"native", "web"} {
		t.Run(kind, func(t *testing.T) {
			db := dbFor(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var actions []func() error
			for i := 0; i < 2; i++ {
				if kind == "native" {
					source := fmt.Sprintf("gap-%d", i)
					task := repository.NativeTask{LogFileName: &source}
					must(t, db.Create(&task).Error)
					q := repository.NewNativeQueries(db, 1<<20)
					must(t, q.Ensure(ctx, task.ID))
					id := task.ID
					actions = append(actions, func() error {
						return q.Process(ctx, id, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
							return io.NopCloser(strings.NewReader(`{"Content":"complete","LogType":1,"LogTime":1}`)), nil
						})
					})
				} else {
					r := repository.NewWebRepository(db)
					task := repository.WebTask{DeviceID: fmt.Sprintf("gap-%d", i), LogDate: time.Now().UnixNano(), PageNum: 1, Content: "[]"}
					must(t, r.Save(ctx, &task))
					must(t, r.Ensure(ctx, task.ID))
					id := task.ID
					actions = append(actions, func() error {
						return r.Process(ctx, id, func(context.Context, repository.WebTask) ([]repository.WebDetail, error) {
							return []repository.WebDetail{{Content: "complete", LogType: 1, LogTime: 1}}, nil
						})
					})
				}
			}
			var arrived int32
			release := make(chan struct{})
			name := "empty-publication-gap"
			must(t, db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table != "logan_log_detail" && tx.Statement.Table != "web_detail" {
					return
				}
				if atomic.AddInt32(&arrived, 1) == 2 {
					close(release)
				}
				select {
				case <-release:
				case <-ctx.Done():
				}
			}))
			defer db.Callback().Create().Remove(name)
			results := make(chan error, 2)
			for _, action := range actions {
				go func(f func() error) { results <- f() }(action)
			}
			for i := 0; i < 2; i++ {
				select {
				case err := <-results:
					if err != nil {
						t.Errorf("independent publication failed: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("publication barrier timed out")
				}
			}
			if atomic.LoadInt32(&arrived) != 2 {
				t.Fatal("both publication inserts not exercised")
			}
		})
	}
}

func TestMySQLPublicationReplacesHistoricalDetails(t *testing.T) {
	for _, kind := range []string{"native", "web"} {
		for _, stale := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stale=%v", kind, stale), func(t *testing.T) {
				db := dbFor(t)
				ctx := context.Background()
				var id int64
				var process func() error
				table := "logan_log_detail"
				steal := func() {
					if stale {
						must(t, db.Exec("UPDATE logan_processing_job SET owner='replacement',fence=fence+1 WHERE kind=? AND task_id=?", kind, id).Error)
					}
				}
				if kind == "native" {
					source := "history"
					task := repository.NativeTask{LogFileName: &source}
					must(t, db.Create(&task).Error)
					id = task.ID
					q := repository.NewNativeQueries(db, 1<<20)
					must(t, q.Ensure(ctx, id))
					process = func() error {
						return q.Process(ctx, id, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
							steal()
							return io.NopCloser(strings.NewReader(`{"Content":"replacement","LogType":1,"LogTime":1}`)), nil
						})
					}
				} else {
					table = "web_detail"
					r := repository.NewWebRepository(db)
					task := repository.WebTask{DeviceID: "history", LogDate: time.Now().UnixNano(), PageNum: 1, Content: "[]"}
					must(t, r.Save(ctx, &task))
					id = task.ID
					must(t, r.Ensure(ctx, id))
					process = func() error {
						return r.Process(ctx, id, func(context.Context, repository.WebTask) ([]repository.WebDetail, error) {
							steal()
							return []repository.WebDetail{{Content: "replacement", LogType: 1, LogTime: 1}}, nil
						})
					}
				}
				for i := 0; i < 501; i++ {
					if kind == "web" {
						must(t, db.Exec("INSERT INTO web_detail(task_id,content,log_type,log_time,add_time,minute_offset) VALUES (?, 'historical',1,1,1,0)", id).Error)
					} else {
						must(t, db.Exec("INSERT INTO logan_log_detail(task_id,content,log_type,log_time) VALUES (?, 'historical',1,1)", id).Error)
					}
				}
				err := process()
				var n int64
				if stale {
					if err == nil {
						t.Fatal("stale publication accepted")
					}
					must(t, db.Table(table).Where("task_id=? AND content='historical'", id).Count(&n).Error)
					if n != 501 {
						t.Fatalf("stale owner removed historical details: %d", n)
					}
				} else {
					must(t, err)
					must(t, db.Table(table).Where("task_id=?", id).Count(&n).Error)
					if n != 1 {
						t.Fatalf("partial historical details not replaced: %d", n)
					}
					must(t, db.Table(table).Where("task_id=? AND content='replacement'", id).Count(&n).Error)
					if n != 1 {
						t.Fatal("replacement content missing")
					}
				}
			})
		}
	}
}
