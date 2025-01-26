package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/gigcodes/launch-agent/config"
	"github.com/gigcodes/launch-agent/logger"
	"github.com/gigcodes/launch-agent/model"
	"github.com/gigcodes/launch-agent/psutil"
	"github.com/spf13/viper"
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
				err := initApplication()
				if err != nil {
					return err
				}

				psutilData, err := psutil.Fetch()
				if err != nil {
					logger.Fatal("Error fetching system stats:", err)
					return nil
				}

				psutil.Pulse(psutilData)
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
