package server

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"serverupdater/internal/model"
)

func Sample(ctx context.Context) model.Metrics {
	m := model.Metrics{SampledAt: time.Now().UTC(), Disks: []model.Disk{}}
	addError := func(component string, err error) {
		if err != nil {
			m.Errors = append(m.Errors, fmt.Sprintf("%s: %v", component, err))
		}
	}
	if values, err := cpu.PercentWithContext(ctx, 0, false); err != nil {
		addError("CPU", err)
	} else if len(values) > 0 {
		m.CPUPercent = values[0]
	}
	if v, err := load.AvgWithContext(ctx); err != nil {
		addError("Load", err)
	} else {
		m.Load1 = v.Load1
		m.Load5 = v.Load5
		m.Load15 = v.Load15
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		addError("RAM", err)
	} else {
		m.RAMTotal = v.Total
		m.RAMAvailable = v.Available
		m.RAMUsedPercent = v.UsedPercent
	}
	var err error
	m.UptimeSeconds, err = host.UptimeWithContext(ctx)
	addError("Laufzeit", err)
	parts, err := disk.PartitionsWithContext(ctx, false)
	addError("Dateisysteme", err)
	local := map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "vfat": true, "exfat": true, "f2fs": true, "jfs": true, "reiserfs": true, "fuseblk": true, "bcachefs": true, "overlay": true}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Mountpoint] || !local[p.Fstype] {
			continue
		}
		seen[p.Mountpoint] = true
		v, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			addError(p.Mountpoint, err)
			continue
		}
		m.Disks = append(m.Disks, model.Disk{Mountpoint: p.Mountpoint, Filesystem: p.Fstype, Total: v.Total, Available: v.Free, UsedPercent: v.UsedPercent})
	}
	if !seen["/"] {
		if v, err := disk.UsageWithContext(ctx, "/"); err != nil {
			addError("/", err)
		} else {
			m.Disks = append(m.Disks, model.Disk{Mountpoint: "/", Total: v.Total, Available: v.Free, UsedPercent: v.UsedPercent})
		}
	}
	sort.Slice(m.Disks, func(i, j int) bool { return m.Disks[i].Mountpoint < m.Disks[j].Mountpoint })
	return m
}
