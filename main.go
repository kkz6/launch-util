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
	"github.com/gigcodes/launch-util/s3cmd"
	"github.com/gigcodes/launch-util/scheduler"
)

const (
	usage = "Launch backup agent — dump databases and files to S3-compatible or local storage."
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
			// One-shot S3 ops (upload / download / delete). Let the
			// platform drive backups, restores, and retention pruning
			// without installing the `aws` CLI on the server — the agent
			// already links the AWS SDK. No config file is read.
			// Credentials come from the environment so they never appear
			// in `ps`.
			Name:  "upload",
			Usage: "Upload a file to S3-compatible storage (creds from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY env)",
			Flags: append(s3CommonFlags(),
				&cli.StringFlag{Name: "file", Required: true, Usage: "Local file to upload"},
				&cli.StringFlag{Name: "key", Required: true, Usage: "Object key within the bucket"},
				&cli.StringFlag{Name: "storage-class", Usage: "Optional S3 storage class"},
			),
			Action: func(ctx *cli.Context) error {
				location, err := s3cmd.Upload(s3ConfigFromCtx(ctx), ctx.String("file"), ctx.String("key"), ctx.String("storage-class"))
				if err != nil {
					return err
				}
				fmt.Println(location)
				return nil
			},
		},
		{
			Name:  "download",
			Usage: "Download an S3 object to a local file (creds from env)",
			Flags: append(s3CommonFlags(),
				&cli.StringFlag{Name: "key", Required: true, Usage: "Object key within the bucket"},
				&cli.StringFlag{Name: "dest", Required: true, Usage: "Local destination path"},
			),
			Action: func(ctx *cli.Context) error {
				return s3cmd.Download(s3ConfigFromCtx(ctx), ctx.String("key"), ctx.String("dest"))
			},
		},
		{
			Name:  "delete",
			Usage: "Delete an S3 object (idempotent — a missing key is a no-op; creds from env)",
			Flags: append(s3CommonFlags(),
				&cli.StringFlag{Name: "key", Required: true, Usage: "Object key within the bucket"},
			),
			Action: func(ctx *cli.Context) error {
				return s3cmd.Delete(s3ConfigFromCtx(ctx), ctx.String("key"))
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

// s3CommonFlags are the destination/auth flags shared by the upload,
// download, and delete commands. Credentials are NOT flags — they come
// from the environment so they never land in `ps`.
func s3CommonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "bucket", Required: true, Usage: "S3 bucket"},
		&cli.StringFlag{Name: "region", Usage: "Region, e.g. us-east-1"},
		&cli.StringFlag{Name: "endpoint", Usage: "Custom S3 endpoint (Spaces / B2 / Wasabi / MinIO)"},
		&cli.BoolFlag{Name: "force-path-style", Usage: "Use path-style addressing (most non-AWS S3)"},
	}
}

func s3ConfigFromCtx(ctx *cli.Context) s3cmd.Config {
	return s3cmd.Config{
		Bucket:          ctx.String("bucket"),
		Region:          ctx.String("region"),
		Endpoint:        ctx.String("endpoint"),
		ForcePathStyle:  ctx.Bool("force-path-style"),
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	}
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
