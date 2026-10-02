package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"petars-dev/govibe/config"
	"petars-dev/govibe/storage/mariadb"
	storagemig "petars-dev/govibe/storage/migrations"
)

func init() {
	rootCmd.AddCommand(migrateCmd)
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
}

func openDBFromConfig() (*sql.DB, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", err
	}
	dbx, err := mariadb.Open(cfg.Database.MariaDB.DSN)
	if err != nil {
		return nil, "", err
	}
	if dbx == nil {
		return nil, "", fmt.Errorf("no DSN configured (MARIADB_DSN)")
	}
	return dbx.DB, cfg.Database.Type, nil
}

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Apply all up migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, dialect, err := openDBFromConfig()
		if err != nil {
			return err
		}
		defer db.Close()
		if err := storagemig.SetDialect(dialect); err != nil {
			return err
		}
		logrus.Info("running goose up")
		goose.SetBaseFS(storagemig.MigrationsFS())
		return goose.UpContext(context.Background(), db, "sql")
	},
}

var migrateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print migration status",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, dialect, err := openDBFromConfig()
		if err != nil {
			return err
		}
		defer db.Close()
		if err := storagemig.SetDialect(dialect); err != nil {
			return err
		}
		goose.SetBaseFS(storagemig.MigrationsFS())
		return goose.Status(db, "sql")
	},
}

var migrateDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Apply one down migration",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, dialect, err := openDBFromConfig()
		if err != nil {
			return err
		}
		defer db.Close()
		if err := storagemig.SetDialect(dialect); err != nil {
			return err
		}
		logrus.Info("running goose down")
		goose.SetBaseFS(storagemig.MigrationsFS())
		return goose.Down(db, "sql")
	},
}

func init() {
	migrateCmd.AddCommand(migrateUpCmd)
	migrateCmd.AddCommand(migrateStatusCmd)
	migrateCmd.AddCommand(migrateDownCmd)
}
