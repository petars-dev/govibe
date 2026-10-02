package main

import (
    "context"
    "net/http"
    "time"

    "github.com/sirupsen/logrus"
    "github.com/workos/workos-go/v10"

    "trace-loop/os/config"
    "trace-loop/os/logger"
    "trace-loop/os/routes"
    storagemig "trace-loop/os/storage/migrations"
    "trace-loop/os/storage/mariadb"
)

func main() {
    // Load configuration from embedded env.yml resolved via environment variables.
    cfg, err := config.Load()
    if err != nil {
        logrus.Fatalf("config: %v", err)
    }

    // Initialize logrus.
    logger.Setup(cfg.Server.LogLevel)
    logrus.WithField("port", cfg.Server.Port).Info("booting server")

    // Initialize optional WorkOS client from config.
    if cfg.WorkOS.APIKey != "" {
        _ = workos.NewClient(cfg.WorkOS.APIKey, workos.WithClientID(cfg.WorkOS.ClientID))
        logrus.Info("workos client initialized")
    }

    // Optional database + migrations (MariaDB/MySQL). If no DSN is provided, we skip DB init.
    if dbx, err := mariadb.Open(cfg.Database.MariaDB.DSN); err != nil {
        logrus.Fatalf("db open: %v", err)
    } else if dbx != nil {
        defer dbx.Close()
        if err := storagemig.Up(context.Background(), dbx.DB, cfg.Database.Type); err != nil {
            logrus.Fatalf("db migrate: %v", err)
        }
        logrus.Info("database connected and migrated")
    }

    r := routes.NewRouter()

    srv := &http.Server{
        Addr:         ":" + cfg.Server.Port,
        Handler:      r,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    logrus.Infof("listening on http://localhost:%s", cfg.Server.Port)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        logrus.Fatalf("server: %v", err)
    }
}
