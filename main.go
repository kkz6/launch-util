package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/gigcodes/launch-agent/config"
	"github.com/gigcodes/launch-agent/logger"
	"github.com/gigcodes/launch-agent/model"
	"github.com/gigcodes/launch-agent/scheduler"
	"github.com/sevlyar/go-daemon"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v3"
)

const (
	usage = "Server monitoring and database backup agent for Gigcodes Launch managed servers"
)

var (
	configFile string
	version    = "0.1"
	signal     = flag.String("s", "", `Send signal to the daemon:
  quit — graceful shutdown
  stop — fast shutdown
  reload — reloading the configuration file`)
)

func buildFlags(flags []cli.Flag) []cli.Flag {
	return append(flags, &cli.StringFlag{
		Name:        "config",
		Aliases:     []string{"c"},
		Usage:       "Special a config file",
		Destination: &configFile,
	})
}

func main() {
	app := &cli.Command{}

	app.Name = "launch-agent"
	app.Version = version
	app.Usage = usage

	daemon.AddCommand(daemon.StringFlag(signal, "quit"), syscall.SIGQUIT, termHandler("pulse"))
	daemon.AddCommand(daemon.StringFlag(signal, "stop"), syscall.SIGTERM, termHandler("pulse"))
	daemon.AddCommand(daemon.StringFlag(signal, "reload"), syscall.SIGHUP, reloadHandler("pulse"))

	daemon.AddCommand(daemon.StringFlag(signal, "quit"), syscall.SIGQUIT, termHandler("backup"))
	daemon.AddCommand(daemon.StringFlag(signal, "stop"), syscall.SIGTERM, termHandler("backup"))
	daemon.AddCommand(daemon.StringFlag(signal, "reload"), syscall.SIGHUP, reloadHandler("backup"))

	app.Commands = []*cli.Command{
		{
			Name: "backup",
			Flags: buildFlags([]cli.Flag{
				&cli.StringSliceFlag{
					Name:    "model",
					Aliases: []string{"m"},
					Usage:   "Model name that you want perform",
				},
			}),
			Action: func(ctx context.Context, cmd *cli.Command) error {
				var modelNames []string
				err := initApplication()
				if err != nil {
					return err
				}
				modelNames = append(cmd.StringSlice("model"), cmd.Args().Slice()...)
				return backup(modelNames)
			},
		},
		{
			Name:    "start:backup",
			Version: "master",
			Usage:   "Does database backups",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				fmt.Println("Launch agent starting...")

				dm := &daemon.Context{
					PidFileName: config.PidFilePath,
					PidFilePerm: 0644,
					WorkDir:     "./",
				}

				d, err := dm.Reborn()
				if err != nil {
					return fmt.Errorf("start failed, please check is there another instance running: %w", err)
				}
				if d != nil {
					return nil
				}

				defer dm.Release() //nolint:errcheck

				logger.SetLogger(config.LogFilePath)

				err = initApplication()
				if err != nil {
					return err
				}

				if err := scheduler.Start(); err != nil {
					return fmt.Errorf("failed to start scheduler: %w", err)
				}

				return nil
			},
		},
		{
			Name:    "start:pulse",
			Version: "master",
			Usage:   "Shares performance information to launch",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				fmt.Println("Launch pulse agent starting...")

				dm := &daemon.Context{
					PidFileName: config.PulsePidFilePath,
					PidFilePerm: 0644,
					WorkDir:     "./",
				}

				d, err := dm.Reborn()
				if err != nil {
					return fmt.Errorf("start failed, please check is there another instance running: %w", err)
				}
				if d != nil {
					return nil
				}

				defer dm.Release() //nolint:errcheck

				logger.SetLogger(config.PulseLogFilePath)

				err = initApplication()
				if err != nil {
					return err
				}

				if err := scheduler.StartPulse(); err != nil {
					return fmt.Errorf("failed to start scheduler: %w", err)
				}

				return nil
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		logger.Fatal(err.Error())
	}
}

func initApplication() error {
	return config.Init(configFile)
}

func backup(modelNames []string) error {
	var models []*model.Model
	if len(modelNames) == 0 {
		// perform all
		models = model.GetModels()
	} else {
		for _, name := range modelNames {
			if m := model.GetModelByName(name); m == nil {
				return fmt.Errorf("model %s not found in %s", name, viper.ConfigFileUsed())
			} else {
				models = append(models, m)
			}
		}
	}

	for _, m := range models {
		if err := m.Perform(); err != nil {
			logger.Tag(fmt.Sprintf("Model %s", m.Config.Name)).Error(err)
		}
	}
	return nil
}

// termHandler for each daemon type (backup or pulse)
func termHandler(daemonType string) func(os.Signal) error {
	return func(sig os.Signal) error {
		if daemonType == "pulse" {
			logger.Info("Received QUIT signal for Pulse, exiting...")
			scheduler.StopPulse()
		} else if daemonType == "backup" {
			logger.Info("Received QUIT signal for Backup, exiting...")
			scheduler.Stop()
		}
		os.Exit(0)
		return nil
	}
}

// reloadHandler for each daemon type (backup or pulse)
func reloadHandler(daemonType string) func(os.Signal) error {
	return func(sig os.Signal) error {
		if daemonType == "pulse" {
			logger.Info("Reloading config for Pulse...")
			err := config.Init(configFile)
			if err != nil {
				logger.Error(err)
			}
			// Restart Pulse related functionality if needed
			scheduler.RestartPulse()
		} else if daemonType == "backup" {
			logger.Info("Reloading config for Backup...")
			err := config.Init(configFile)
			if err != nil {
				logger.Error(err)
			}
			// Restart Backup related functionality if needed
			scheduler.Restart()
		}
		return nil
	}
}
