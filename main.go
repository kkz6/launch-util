package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/gigcodes/launch-agent/config"
	"github.com/gigcodes/launch-agent/logger"
	"github.com/gigcodes/launch-agent/psutil"
	"github.com/urfave/cli/v3"
	"os"
)

const (
	usage = "Server monitoring and database backup agent for Gigcodes Launch managed servers"
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

func main() {
	app := &cli.Command{}

	app.Name = "launch-agent"
	app.Version = version
	app.Usage = usage

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
			Name:    "pulse",
			Version: "master",
			Usage:   "Show resources usages",
			Action: func(ctx context.Context, cmd *cli.Command) error {
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
	fmt.Println(modelNames)
	return nil
}
