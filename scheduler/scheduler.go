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
	mycron     *gocron.Scheduler
	dbcron     *gocron.Scheduler
	pulseMutex sync.Mutex
	modelMutex sync.Mutex
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

	dbcron = gocron.NewScheduler(time.Local)

	if config.Pulse.Enabled {
		logger.Info("Launch pulse initiated")
		if _, err := mycron.Every(5).Minutes().Do(func() {
			defer pulseMutex.Unlock()

			pulseMutex.Lock()
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
			scheduler = dbcron.Cron(modelConfig.Schedule.Cron)
		} else {
			scheduler = dbcron.Every(modelConfig.Schedule.Every)
			if len(modelConfig.Schedule.At) > 0 {
				scheduler = scheduler.At(modelConfig.Schedule.At)
			} else {
				// If no $at present, delay start cron job with $eveny duration
				startDuration, _ := time.ParseDuration(modelConfig.Schedule.Every)
				scheduler = scheduler.StartAt(time.Now().Add(startDuration))
			}
		}

		if _, err := scheduler.Do(func(modelConfig config.ModelConfig) {
			defer modelMutex.Unlock()
			logger := superlogger.Tag(fmt.Sprintf("Scheduler: %s", modelConfig.Name))

			logger.Info("Performing...")

			m := model.Model{
				Config: modelConfig,
			}
			modelMutex.Lock()
			if err := m.Perform(); err != nil {
				logger.Errorf("Failed to perform: %s", err.Error())
			}
			logger.Info("Done.")
		}, modelConfig); err != nil {
			logger.Errorf("Failed to register job func: %s", err.Error())
		}
	}

	mycron.StartAsync()
	dbcron.StartAsync()

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
		dbcron.Stop()
	}
}
