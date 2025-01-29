package scheduler

import (
	"fmt"
	"github.com/gigcodes/launch-agent/model"
	"github.com/gigcodes/launch-agent/psutil"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gigcodes/launch-agent/config"
	superlogger "github.com/gigcodes/launch-agent/logger"
	"github.com/go-co-op/gocron"
)

var (
	mycron *gocron.Scheduler
	dbcron *gocron.Scheduler
)

func init() {
	config.OnConfigChange(func(in fsnotify.Event) {
		err := Restart()
		if err != nil {
			return
		}
		errT := RestartPulse()
		if errT != nil {
			return
		}
	})
}

// Start scheduler
func Start() error {
	logger := superlogger.Tag("Scheduler")

	dbcron = gocron.NewScheduler(time.Local)

	db := sync.Mutex{}

	for _, modelConfig := range config.Models {
		if !modelConfig.Schedule.Enabled {
			continue
		}

		logger.Info(fmt.Sprintf("Register %s with (%s)", modelConfig.Name, modelConfig.Schedule.String()))

		var scheduler *gocron.Scheduler
		if modelConfig.Schedule.Cron != "" {
			scheduler = dbcron.Cron(modelConfig.Schedule.Cron)
		} else {
			scheduler = dbcron.Every(modelConfig.Schedule.Every)
			if len(modelConfig.Schedule.At) > 0 {
				scheduler = scheduler.At(modelConfig.Schedule.At)
			} else {
				// If no $at present, delay start cron job with $every duration
				startDuration, _ := time.ParseDuration(modelConfig.Schedule.Every)
				scheduler = scheduler.StartAt(time.Now().Add(startDuration))
			}
		}

		if _, err := scheduler.Do(func(modelConfig config.ModelConfig) {
			defer db.Unlock()
			logger := superlogger.Tag(fmt.Sprintf("Scheduler: %s", modelConfig.Name))

			logger.Info("Performing...")

			m := model.Model{
				Config: modelConfig,
			}
			db.Lock()
			if err := m.Perform(); err != nil {
				logger.Errorf("Failed to perform: %s", err.Error())
			}
			logger.Info("Done.")
		}, modelConfig); err != nil {
			logger.Errorf("Failed to register job func: %s", err.Error())
		}
	}

	dbcron.StartAsync()

	return nil
}

func StartPulse() error {
	logger := superlogger.Tag("Pulse Scheduler")

	mycron = gocron.NewScheduler(time.Local)

	mu := sync.Mutex{}

	if config.Pulse.Enabled {
		logger.Info("Launch pulse initiated")

		if _, err := mycron.Every("5s").StartImmediately().Do(func() {
			defer mu.Unlock()

			mu.Lock()
			logger.Info("Launch pulse initiated")

			psutilData, err := psutil.Fetch()

			if err != nil {
				logger.Fatal("Error fetching system stats:", err)
			}

			psutil.Pulse(psutilData)
		}); err != nil {
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

func RestartPulse() error {
	logger := superlogger.Tag("Pulse Scheduler")
	logger.Info("Reloading...")
	StopPulse()
	return StartPulse()
}

func Stop() {
	if dbcron != nil {
		dbcron.Stop()
	}
}

func StopPulse() {
	if mycron != nil {
		mycron.Stop()
	}
}
