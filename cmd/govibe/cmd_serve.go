package main

import (
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"petars-dev/govibe/config"
	"petars-dev/govibe/logger"
	"petars-dev/govibe/routes"
)

func init() { rootCmd.AddCommand(serveCmd) }

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the GoVibe HTTP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		logger.Setup(cfg.Server.LogLevel)

		r := routes.NewRouter()
		srv := &http.Server{
			Addr:         ":" + cfg.Server.Port,
			Handler:      r,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		logrus.Infof("listening on http://localhost:%s", cfg.Server.Port)
		return srv.ListenAndServe()
	},
}
