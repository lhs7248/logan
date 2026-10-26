// Command workbench starts the independent investigation UI/API. It does not
// modify the compatibility service, initialize databases, or contact providers.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"logan/servergo/internal/workbench"
	"logan/servergo/internal/workbench/connectors"
	"logan/servergo/internal/workbenchui"
)

func main() {
	if err := run(); err != nil {
		log.Print("workbench stopped: ", err)
		os.Exit(1)
	}
}
func run() error {
	authPath := os.Getenv("WORKBENCH_AUTH_FILE")
	if authPath == "" {
		return errors.New("WORKBENCH_AUTH_FILE is required")
	}
	auth, err := os.ReadFile(authPath)
	if err != nil {
		return errors.New("cannot read workbench auth configuration")
	}
	var cfg workbench.Config
	if json.Unmarshal(auth, &cfg) != nil || len(cfg.Tokens) == 0 || len(cfg.Projects) == 0 {
		return errors.New("invalid auth configuration: projects and tokens required")
	}
	if err := validateAuth(cfg); err != nil {
		return err
	}
	addr := os.Getenv("WORKBENCH_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8091"
	}
	if err := validateProfile(os.Getenv("WORKBENCH_PROFILE"), authPath, addr, os.Getenv("WORKBENCH_INTRANET_GATEWAY_ACK")); err != nil {
		return err
	}
	if err := workbench.ValidateSDKConfig(cfg); err != nil {
		return err
	}
	cfg.Dir = os.Getenv("WORKBENCH_DATA_DIR")
	if cfg.Dir == "" {
		cfg.Dir = "workbench-data"
	}
	if err := validateDataDir(os.Getenv("WORKBENCH_PROFILE"), cfg.Dir); err != nil {
		return err
	}
	repos := map[string]string{}
	if value := os.Getenv("WORKBENCH_SOURCE_REPOS"); value != "" {
		if json.Unmarshal([]byte(value), &repos) != nil {
			return errors.New("WORKBENCH_SOURCE_REPOS must be a JSON repository ID to local canonical directory map")
		}
	}
	pushProjects := []string{}
	for _, project := range cfg.Projects {
		pushProjects = append(pushProjects, project.ID)
	}
	cfg.Push, err = connectors.LoadPushConfig(os.Getenv("WORKBENCH_PUSH_CONFIG_FILE"), pushProjects)
	if err != nil {
		return errors.New("invalid internal push configuration")
	}
	cfg.Source, err = connectors.NewSource(connectors.SourceConfig{Repositories: repos})
	if err != nil {
		return errors.New("invalid source connector configuration")
	}
	cfg.Model, err = connectors.NewModel(connectors.ModelConfig{Endpoint: os.Getenv("WORKBENCH_MODEL_ENDPOINT"), APIKey: os.Getenv("WORKBENCH_MODEL_API_KEY"), Model: os.Getenv("WORKBENCH_MODEL")})
	if err != nil {
		return errors.New("invalid model connector configuration")
	}
	protocolConfig := connectors.ProtocolConfig{EnableWeb: os.Getenv("WORKBENCH_WEB_PROTOCOL_ENABLED") == "true"}
	if value := os.Getenv("WORKBENCH_NATIVE_KEY_BASE64"); value != "" {
		protocolConfig.NativeKey, err = base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("invalid native key encoding")
		}
	}
	if value := os.Getenv("WORKBENCH_NATIVE_IV_BASE64"); value != "" {
		protocolConfig.NativeIV, err = base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("invalid native IV encoding")
		}
	}
	if path := os.Getenv("WORKBENCH_WEB_PRIVATE_KEY_FILE"); path != "" {
		protocolConfig.WebPrivateKey, err = os.ReadFile(path)
		if err != nil {
			return errors.New("cannot read web private key")
		}
	}
	cfg.Protocols, err = connectors.NewProtocols(protocolConfig)
	if err != nil {
		return errors.New("invalid protocol configuration")
	}
	legacyConfig := connectors.LegacyConfig{}
	if path := os.Getenv("WORKBENCH_LEGACY_CONFIG_FILE"); path != "" {
		b, e := os.ReadFile(path)
		if e != nil || json.Unmarshal(b, &legacyConfig) != nil {
			return errors.New("invalid legacy configuration file")
		}
	}
	cfg.Legacy, err = connectors.NewLegacy(legacyConfig)
	if err != nil {
		return errors.New("invalid legacy connector configuration")
	}
	if dsn := os.Getenv("WORKBENCH_MYSQL_DSN"); dsn != "" {
		db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Discard})
		if e != nil {
			return errors.New("cannot connect optional workbench database")
		}
		namespace := os.Getenv("WORKBENCH_NAMESPACE")
		if namespace == "" {
			namespace = "default"
		}
		cfg.Store, e = workbench.NewMySQLStore(db, namespace)
		if e != nil {
			return errors.New("cannot acquire optional workbench database lease")
		}
	}
	s, err := workbench.Open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.HealthHTTP)
	mux.HandleFunc("/readyz", s.HealthHTTP)
	mux.Handle(workbench.Base+"/", s)
	mux.Handle("/", workbenchui.Handler())
	httpServer := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopped)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()
	log.Printf("workbench listening on %s", addr)
	if err = httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
