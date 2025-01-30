package scheduler

import (
	"fmt"
	"github.com/gigcodes/launch-util/psutil"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gigcodes/launch-util/config"
	superlogger "github.com/gigcodes/launch-util/logger"
	"github.com/gigcodes/launch-util/model"
	"github.com/go-co-op/gocron"
)

var (
	mycron *gocron.Scheduler
)

func init() {
	config.OnConfigChange(func(in fsnotify.Event) {
		Restart()
	})
}

// Start scheduler
func Start() error {
	logger := superlogger.Tag("Scheduler")

	mycron = gocron.NewScheduler(time.Local)

	mu := sync.Mutex{}

	if config.Pulse.Enabled {
		logger.Info("Launch pulse initiated")

		if _, err := mycron.Every(5).Minutes().StartImmediately().Do(func() {
			psutilData, err := psutil.Fetch()

			if err != nil {
				logger.Fatal("Error fetching system stats:", err)
			}

			psutil.Pulse(psutilData)
		}); err != nil {
			logger.Errorf("Failed to register job func: %s", err.Error())
		}
	}

	for _, modelConfig := range config.Models {
		if !modelConfig.Schedule.Enabled {
			continue
		}

		logger.Info(fmt.Sprintf("Register %s with (%s)", modelConfig.Name, modelConfig.Schedule.String()))

		var scheduler *gocron.Scheduler
		if modelConfig.Schedule.Cron != "" {
			scheduler = mycron.Cron(modelConfig.Schedule.Cron)
		} else {
			continue
		}

		if _, err := scheduler.Do(func(modelConfig config.ModelConfig) {
			defer mu.Unlock()
			logger := superlogger.Tag(fmt.Sprintf("Scheduler: %s", modelConfig.Name))

			logger.Info("Performing...")

			m := model.Model{
				Config: modelConfig,
			}
			mu.Lock()
			if err := m.Perform(); err != nil {
				logger.Errorf("Failed to perform: %s", err.Error())
			}
			logger.Info("Done.")
		}, modelConfig); err != nil {
			logger.Errorf("Failed to register job func: %s", err.Error())
		}
	}

	mycron.StartAsync()

	return nil
}

func Restart() error {
	logger := superlogger.Tag("Scheduler")
	logger.Info("Reloading...")
	Stop()
	return Start()
}

func Stop() {
	if mycron != nil {
		mycron.Stop()
	}
}
