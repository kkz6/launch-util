package psutil

import (
	"fmt"
	"github.com/gigcodes/launch-util/config"
	"github.com/gigcodes/launch-util/notifier"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"log"
	"strconv"
	"time"
)

type Psutil struct {
	Load        float64
	DiskTotal   string
	DiskFree    string
	DiskUsed    string
	MemoryTotal string
	MemoryFree  string
	MemoryUsed  string
}

func Fetch() (*Psutil, error) {
	// Fetch system load average
	loads, err := cpu.Percent(time.Second, false)
	if err != nil {
		log.Println("Error fetching CPU load average:", err)
		return nil, err
	}

	// Fetch memory usage
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		log.Println("Error fetching virtual memory:", err)
		return nil, err
	}

	// Fetch disk usage
	partitions, err := disk.Partitions(false)
	if err != nil {
		log.Println("Error fetching disk partitions:", err)
		return nil, err
	}

	// Assuming we are interested in the first partition's usage (usually '/' or the main partition)
	usageStat, err := disk.Usage(partitions[0].Mountpoint)
	if err != nil {
		log.Println("Error fetching disk usage:", err)
		return nil, err
	}

	// Construct the Psutil struct with the fetched data
	psutil := &Psutil{
		Load:        loads[0],
		DiskTotal:   strconv.FormatUint(usageStat.Total, 10),
		DiskFree:    strconv.FormatUint(usageStat.Free, 10),
		DiskUsed:    strconv.FormatUint(usageStat.Used, 10),
		MemoryTotal: strconv.FormatUint(vmStat.Total, 10),
		MemoryFree:  strconv.FormatUint(vmStat.Free, 10),
		MemoryUsed:  strconv.FormatUint(vmStat.Used, 10),
	}

	return psutil, nil
}

func Pulse(data *Psutil) {
	webhook := notifier.NewWebhook(config.Pulse.Webhook)

	payload := map[string]interface{}{
		"event": "pulse",
		"data":  data,
	}
	err := webhook.Notify(payload)
	if err != nil {
		fmt.Println("Error sending pulse notification:", err)
	}
}
