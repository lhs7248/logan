package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/adaptor"
	hconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"log/slog"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/router"
	"logan/servergo/internal/config"
	"logan/servergo/internal/maintenance"
	"logan/servergo/internal/native"
	"logan/servergo/internal/observability"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"logan/servergo/internal/tempfiles"
	"logan/servergo/internal/weblog"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

type application struct {
	jobSnapshot     func(context.Context) ([]repository.JobCounts, error)
	connections     *connections
	draining        *atomic.Bool
	metrics         *observability.Registry
	session         *tempfiles.Session
	maintenance     *maintenance.Runner
	exitWait        time.Duration
	tempBase        string
	maintenanceRepo maintenance.Repository
	scope           string
	orphanAge       time.Duration
	batch           int
	server          *server.Hertz
	pool            *sql.DB
	oss             *storage.OSS
	web             *weblog.Service
	nativeQueries   *native.QueryService
}

func newApplication(ctx context.Context, c config.Config) (*application, error) {
	db, pool, err := repository.Open(ctx, c.Database)
	if err != nil {
		return nil, err
	}
	var session *tempfiles.Session
	var cleanupRepo maintenance.Repository
	var runner *maintenance.Runner
	var metrics *observability.Registry
	if c.MetricsEnabled {
		metrics = observability.New()
	}
	tempBase := c.Native.TempDir
	success := false
	defer func() {
		if !success {
			if session != nil {
				_ = session.Close()
			}
			if pool != nil {
				_ = pool.Close()
			}
		}
	}()
	if c.MaintenanceEnabled {
		cleanupRepo = repository.NewMaintenance(db, maintenance.Scope(c.OSS))
		check, cancel := context.WithTimeout(ctx, c.Database.PingTimeout)
		e := cleanupRepo.Validate(check)
		cancel()
		if e != nil {
			return nil, fmt.Errorf("maintenance initialization failed; verify migration 002")
		}
		session, err = tempfiles.Open(tempBase)
		if err != nil {
			return nil, err
		}
		c.Native.TempDir = session.Path()
	}
	var handlers *handler.Native
	var ossStore *storage.OSS
	var observedStore storage.Store
	if c.Native.Enabled {
		ossStore, err = storage.NewOSS(c.OSS)
		if err != nil {
			if pool != nil {
				_ = pool.Close()
			}
			return nil, err
		}
		observedStore = observability.NewObservedStore(ossStore, metrics)
		var objectStore storage.Store = observedStore
		var tasks native.TaskRepository = repository.NewNativeTasks(db)
		if c.MaintenanceEnabled {
			scope := maintenance.Scope(c.OSS)
			objectStore = maintenance.NewTrackedStore(observedStore, cleanupRepo, scope)
			tasks = repository.NewTrackedNativeTasks(db, scope)
			m := c.Maintenance
			runner, err = maintenance.NewRunner(cleanupRepo, observedStore, maintenance.Config{Scope: scope, Interval: m.Interval, RunTimeout: m.RunTimeout, LeaseTTL: m.LeaseTTL, ObjectLeaseTTL: m.ObjectLeaseTTL, Retention: m.Retention, OrphanAge: m.OrphanAge, RetryDelay: m.RetryDelay, Batch: m.Batch, OnError: func(error) { slog.Warn("maintenance cycle failed") }, Observe: func(op, result string) {
				if metrics != nil {
					metrics.Maintenance(op, result)
				}
			}})
			if err != nil {
				ossStore.Close()
				return nil, err
			}
		}
		service, serviceErr := native.NewService(c.Native, c.Timezone, objectStore, tasks)
		if serviceErr != nil {
			ossStore.Close()
			if pool != nil {
				_ = pool.Close()
			}
			return nil, serviceErr
		}
		handlers = handler.NewNative(service, c.Native.Timeout)
	}
	var webService *weblog.Service
	var webHandler *handler.Web
	if c.Web.Enabled {
		repo := repository.NewWebRepository(db, int64(c.MaxRequestBody))
		validationCtx, cancel := context.WithTimeout(ctx, c.Database.PingTimeout)
		webErr := repo.Validate(validationCtx)
		cancel()
		if webErr == nil {
			webService, webErr = weblog.NewService(weblog.Config{MaxBytes: int64(c.MaxRequestBody), MaxTasks: c.Web.MaxTasks, Workers: c.Web.Workers, Timeout: c.Web.Timeout, Wait: c.Web.Wait, Timezone: c.Timezone}, protocol.NewWebDecoder(c.Web.PrivateKey, int64(c.MaxRequestBody)), repo)
		}
		if webErr != nil {
			if ossStore != nil {
				ossStore.Close()
			}
			if pool != nil {
				_ = pool.Close()
			}
			return nil, fmt.Errorf("Web processing initialization failed; verify migration and configuration")
		}
		webHandler = handler.NewWeb(webService, int64(c.MaxRequestBody), c.Web.Timeout)
	}
	var nativeQueries *native.QueryService
	var queryHandler *handler.NativeQueries
	if c.NativeQuery.Enabled {
		repo := repository.NewNativeQueries(db, c.NativeQuery.MaxPlain)
		checkCtx, cancel := context.WithTimeout(ctx, c.Database.PingTimeout)
		queryErr := repo.Validate(checkCtx)
		cancel()
		if queryErr == nil {
			nativeQueries, queryErr = native.NewQueryService(native.QueryConfig{MaxCompressed: c.Native.MaxInput, MaxPlain: c.NativeQuery.MaxPlain, MaxStaged: c.NativeQuery.MaxStaged, MaxLine: c.NativeQuery.MaxLine, MaxRecords: c.NativeQuery.MaxRecords, MaxIDs: c.NativeQuery.MaxIDs, Workers: c.NativeQuery.Workers, Timeout: c.NativeQuery.Timeout, Wait: c.NativeQuery.Wait, TempDir: c.Native.TempDir}, observedStore, repo)
		}
		if queryErr != nil {
			if ossStore != nil {
				ossStore.Close()
			}
			if pool != nil {
				_ = pool.Close()
			}
			return nil, fmt.Errorf("native query initialization failed; verify migration and configuration")
		}
		queryHandler = handler.NewNativeQueries(nativeQueries, c.NativeQuery.Timeout)
	}
	if metrics != nil {
		if webService != nil {
			webService.SetJobObserver(metrics.ObserveJob)
		}
		if nativeQueries != nil {
			nativeQueries.SetJobObserver(metrics.ObserveJob)
		}
	}
	conns := &connections{}
	draining := &atomic.Bool{}
	h := newServerRuntime(c, handlers, webHandler, conns, draining, metrics, queryHandler)
	var snapshot func(context.Context) ([]repository.JobCounts, error)
	if metrics != nil && (c.Web.Enabled || c.NativeQuery.Enabled) {
		snapshot = func(ctx context.Context) ([]repository.JobCounts, error) {
			return repository.ProcessingJobSnapshot(ctx, db)
		}
	}
	success = true
	return &application{jobSnapshot: snapshot, server: h, pool: pool, oss: ossStore, web: webService, nativeQueries: nativeQueries, connections: conns, draining: draining, metrics: metrics, session: session, maintenance: runner, exitWait: c.ExitWait, tempBase: tempBase, maintenanceRepo: cleanupRepo, scope: maintenance.Scope(c.OSS), orphanAge: c.Maintenance.OrphanAge, batch: c.Maintenance.Batch}, nil
}
func serverOptions(c config.Config) []hconfig.Option {
	bodyThreshold := c.MaxRequestBody
	readTimeout := c.ReadTimeout
	if c.Native.Enabled || c.Web.Enabled {
		bodyThreshold = min(bodyThreshold, 64*1024)
		if c.Native.Enabled {
			readTimeout = min(readTimeout, c.Native.Timeout)
		}
		if c.Web.Enabled {
			readTimeout = min(readTimeout, c.Web.Timeout)
		}
	}
	return []hconfig.Option{
		server.WithHostPorts(c.Address), server.WithTransport(standard.NewTransporter),
		server.WithReadTimeout(readTimeout), server.WithWriteTimeout(c.WriteTimeout),
		server.WithIdleTimeout(c.IdleTimeout), server.WithExitWaitTime(c.ExitWait),
		server.WithMaxRequestBodySize(bodyThreshold), server.WithStreamBody(c.Native.Enabled || c.Web.Enabled), server.WithDisablePreParseMultipartForm(c.Native.Enabled || c.Web.Enabled),
	}
}
func newServer(c config.Config, natives ...*handler.Native) *server.Hertz {
	var n *handler.Native
	if len(natives) > 0 {
		n = natives[0]
	}
	return newServerWithWeb(c, n, nil)
}
func newServerWithWeb(c config.Config, n *handler.Native, w *handler.Web, queries ...*handler.NativeQueries) *server.Hertz {
	return newServerRuntime(c, n, w, &connections{}, &atomic.Bool{}, nil, queries...)
}
func newServerRuntime(c config.Config, n *handler.Native, w *handler.Web, conns *connections, draining *atomic.Bool, metrics *observability.Registry, queries ...*handler.NativeQueries) *server.Hertz {
	options := append(serverOptions(c), server.WithTransport(conns.transport))
	h := server.Default(options...)
	if metrics != nil {
		h.Use(metrics.HTTPMiddleware())
	}
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if draining.Load() {
			c.Response.Header.SetConnectionClose(true)
			c.AbortWithStatus(503)
			return
		}
		defer func() {
			if draining.Load() {
				c.Response.Header.SetConnectionClose(true)
			}
		}()
		c.Next(ctx)
	})
	if metrics != nil {
		h.GET("/metrics", adaptor.HertzHandler(metrics.HTTPHandler()))
	}
	if c.Native.Enabled || c.Web.Enabled {
		paths := []string{}
		if n != nil {
			paths = append(paths, c.ContextPath+"/logan/upload.json")
		}
		if w != nil {
			paths = append(paths, c.ContextPath+"/logan/web/upload.json")
		}
		h.Use(handler.StreamBodyGuard(paths...))
	}
	h.GET("/livez", func(_ context.Context, ctx *app.RequestContext) { ctx.JSON(200, map[string]string{"status": "alive"}) })
	// The foundation cannot serve business traffic, even when MySQL is connected.
	h.GET("/readyz", func(_ context.Context, ctx *app.RequestContext) {
		ctx.JSON(503, map[string]string{"status": "not_ready", "reason": "integration and production validation pending"})
	})
	register(h)
	router.RegisterLegacyWithWeb(h, c.ContextPath, n, w, queries...)
	return h
}
func (a *application) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return a.RunContext(ctx)
}
func (a *application) RunContext(ctx context.Context) error {
	workersCtx, cancelWorkers := context.WithCancel(context.Background())
	maintenanceCtx, cancelMaintenance := context.WithCancel(context.Background())
	maintenanceDone := make(chan struct{})
	maintenanceStarted := false
	stopped := make(chan error, 1)
	go func() { stopped <- a.server.Run() }()
	var runErr error
	// Wait for initialization before initiating shutdown; an already canceled
	// caller must not leave a listener starting after resources were closed.
	for !a.server.IsRunning() {
		select {
		case runErr = <-stopped:
			goto shutdown
		case <-time.After(time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		goto shutdown
	}
	if a.web != nil {
		a.web.Start(workersCtx)
	}
	if a.nativeQueries != nil {
		a.nativeQueries.Start(workersCtx)
	}
	go func() { defer close(maintenanceDone); a.background(maintenanceCtx) }()
	maintenanceStarted = true
	select {
	case runErr = <-stopped:
	case <-ctx.Done():
	}
shutdown:
	if !maintenanceStarted {
		close(maintenanceDone)
	}
	a.draining.Store(true)
	cancelMaintenance()
	wait := a.exitWait
	if wait <= 0 {
		wait = time.Minute
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_ = a.server.Shutdown(shutdownCtx)
	if a.web != nil {
		a.web.StopClaims()
	}
	if a.nativeQueries != nil {
		a.nativeQueries.StopClaims()
	}
	if a.web != nil {
		_ = a.web.Wait(shutdownCtx)
	}
	if a.nativeQueries != nil {
		_ = a.nativeQueries.Wait(shutdownCtx)
	}
	a.connections.closeAll()
	cancelWorkers()
	a.connections.wait()
	if a.nativeQueries != nil {
		a.nativeQueries.Close()
	}
	if a.web != nil {
		a.web.Close()
	}
	<-maintenanceDone
	if a.oss != nil {
		a.oss.Close()
	}
	if a.pool != nil {
		if err := a.pool.Close(); err != nil {
			slog.Warn("database pool cleanup failed")
		}
	}
	if a.session != nil {
		if err := a.session.Close(); err != nil {
			slog.Warn("temporary session cleanup failed")
		}
	}
	return runErr
}
func (a *application) background(ctx context.Context) {
	var done chan struct{}
	if a.maintenance != nil {
		done = make(chan struct{})
		go func() {
			defer close(done)
			if err := a.maintenance.Run(ctx); err != nil && ctx.Err() == nil {
				slog.Error("maintenance stopped unexpectedly")
				if a.metrics != nil {
					a.metrics.Maintenance("cycle", "error")
				}
			}
		}()
		defer func() { <-done }()
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	sweep := time.NewTicker(time.Hour)
	defer sweep.Stop()
	sample := func() {
		if a.metrics != nil {
			if a.jobSnapshot != nil {
				c, cancel := context.WithTimeout(ctx, 3*time.Second)
				jobs, e := a.jobSnapshot(c)
				cancel()
				if e == nil {
					for _, job := range jobs {
						a.metrics.SetJobStates(job.Kind, job.Pending, job.Running, job.Failed)
					}
				} else if ctx.Err() == nil {
					a.metrics.Maintenance("scan", "error")
				}
			}
			if a.pool != nil {
				a.metrics.SetDBStats(a.pool.Stats())
			}
			if a.maintenanceRepo != nil {
				c, cancel := context.WithTimeout(ctx, 3*time.Second)
				v, e := a.maintenanceRepo.Snapshot(c, a.scope)
				cancel()
				if e != nil && ctx.Err() == nil {
					a.metrics.Maintenance("scan", "error")
				}
				if e == nil {
					a.metrics.SetMaintenanceSnapshot(v.Pending, v.Deleting, v.Uploading, v.Failed)
				}
			}
		}
	}
	clean := func() {
		if a.session != nil {
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, e := tempfiles.Sweep(c, a.tempBase, a.orphanAge, a.batch, time.Now())
			if e != nil && ctx.Err() == nil {
				slog.Warn("temporary session sweep failed")
			}
			cancel()
			if a.metrics != nil {
				result := "success"
				if e != nil {
					result = "error"
				}
				a.metrics.Maintenance("temp_sweep", result)
			}
		}
	}
	sample()
	clean()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		case <-sweep.C:
			clean()
		}
	}
}
