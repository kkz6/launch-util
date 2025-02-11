package main

import (
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/sevlyar/go-daemon"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"

	"github.com/gigcodes/launch-util/config"
	"github.com/gigcodes/launch-util/logger"
	"github.com/gigcodes/launch-util/model"
	"github.com/gigcodes/launch-util/psutil"
	"github.com/gigcodes/launch-util/rpc"
	"github.com/gigcodes/launch-util/scheduler"
)

const (
	usage = "Backup your databases, files to FTP / SCP / S3 / GCS and other cloud storages."
)

var (
	configFile string
	version    = "master"
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

func termHandler(sig os.Signal) error {
	logger.Info("Received QUIT signal, exiting...")
	scheduler.Stop()
	os.Exit(0)
	return nil
}

func reloadHandler(sig os.Signal) error {
	logger.Info("Reloading config...")
	err := config.Init(configFile)
	if err != nil {
		logger.Error(err)
	}

	return nil
}

func main() {
	app := cli.NewApp()

	app.Version = version
	app.Name = "launch-agent"
	app.Usage = usage

	daemon.AddCommand(daemon.StringFlag(signal, "quit"), syscall.SIGQUIT, termHandler)
	daemon.AddCommand(daemon.StringFlag(signal, "stop"), syscall.SIGTERM, termHandler)
	daemon.AddCommand(daemon.StringFlag(signal, "reload"), syscall.SIGHUP, reloadHandler)

	app.Commands = []*cli.Command{
		{
			Name: "perform",
			Flags: buildFlags([]cli.Flag{
				&cli.StringSliceFlag{
					Name:    "model",
					Aliases: []string{"m"},
					Usage:   "Model name that you want perform",
				},
			}),
			Action: func(ctx *cli.Context) error {
				var modelNames []string
				err := initApplication()
				if err != nil {
					return err
				}
				modelNames = append(ctx.StringSlice("model"), ctx.Args().Slice()...)
				return perform(modelNames)
			},
		},
		{
			Name:  "pulse",
			Usage: "Show resources usages",
			Action: func(ctx *cli.Context) error {
				err := initApplication()
				if err != nil {
					return err
				}
				psutilData, err := psutil.Fetch()
				if err != nil {
					logger.Fatal("Error fetching system stats:", err)
					return nil
				}
				fmt.Printf("System Stats:\n")
				fmt.Printf("Load Average (1 min): %.2f\n", psutilData.Load)
				fmt.Printf("Disk Total: %s bytes\n", psutilData.DiskTotal)
				fmt.Printf("Disk Free: %s bytes\n", psutilData.DiskFree)
				fmt.Printf("Disk Used: %s bytes\n", psutilData.DiskUsed)
				fmt.Printf("Memory Total: %s bytes\n", psutilData.MemoryTotal)
				fmt.Printf("Memory Free: %s bytes\n", psutilData.MemoryFree)
				fmt.Printf("Memory Used: %s bytes\n", psutilData.MemoryUsed)

				psutil.Pulse(psutilData)
				return nil
			},
		},
		{
			Name:  "start",
			Usage: "Start as daemon",
			Flags: buildFlags([]cli.Flag{}),
			Action: func(ctx *cli.Context) error {
				fmt.Println("Launch agent starting...")

				args := []string{"launch-agent", "run"}
				if len(configFile) != 0 {
					args = append(args, "--config", configFile)
				}

				dm := &daemon.Context{
					PidFileName: config.PidFilePath,
					PidFilePerm: 0644,
					WorkDir:     "./",
					Args:        args,
				}

				d, err := dm.Reborn()
				if err != nil {
					return fmt.Errorf("start failed, please check is there another instance running: %w", err)
				}
				if d != nil {
					return nil
				}
				defer dm.Release() //nolint:errcheck

				return nil
			},
		},
		{
			Name:  "run",
			Usage: "Run Launch Agent",
			Flags: buildFlags([]cli.Flag{}),
			Action: func(ctx *cli.Context) error {
				logger.SetLogger(config.LogFilePath)

				err := initApplication()
				if err != nil {
					return err
				}

				if err := scheduler.Start(); err != nil {
					return fmt.Errorf("failed to start scheduler: %w", err)
				}

				select {}
			},
		},
		{
			Name:  "status",
			Usage: "Check Supervisor daemon statuses and send webhook response",
			Flags: buildFlags([]cli.Flag{
				&cli.StringSliceFlag{
					Name:    "id",
					Aliases: []string{"i"},
					Usage:   "Supervisor daemon IDs to check (if not provided, status for all daemons will be retrieved)",
				},
				&cli.StringFlag{
					Name:  "supervisor",
					Usage: "Supervisor XML-RPC endpoint",
					Value: "http://localhost/RPC2",
				},
			}),
			Action: func(ctx *cli.Context) error {
				daemonIDs := ctx.StringSlice("id")
				socketPath := "/var/run/supervisor.sock"
				rpcEndpoint := ctx.String("supervisor")

				err := initApplication()
				if err != nil {
					return err
				}

				// Call the function from the rpc package to check statuses and send webhook.
				if err := rpc.SendDaemonStatus(daemonIDs, socketPath, rpcEndpoint); err != nil {
					return fmt.Errorf("failed to send daemon status: %w", err)
				}
				return nil
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		logger.Fatal(err.Error())
	}
}

func initApplication() error {
	return config.Init(configFile)
}

func perform(modelNames []string) error {
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
