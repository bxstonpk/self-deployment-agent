// Module T resource sampling (FR-090): what the container runtime itself
// reports about every application container the platform started. The
// containers are found by the labels Module S already puts on them, so a
// container the platform didn't start — the platform's own database
// containers included — is never sampled.
package runtimeengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"platform-api/internal/domain"
)

// SampleUsage returns one reading per running application container. A
// container whose stats can't be read is left out — FR-090's gap, rather
// than a zero that would read as "idle".
func (r *DockerRuntime) SampleUsage(ctx context.Context) ([]domain.ContainerUsage, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", labelApplication)),
	})
	if err != nil {
		return nil, fmt.Errorf("list application containers: %w", err)
	}
	out := make([]domain.ContainerUsage, 0, len(list))
	for _, c := range list {
		usage, err := r.usageOf(ctx, c.ID)
		if err != nil {
			// A container that ended between being listed and being read
			// is the ordinary case during a restart, a scale-down or a
			// delete: a gap, and not worth saying anything about.
			if !errors.Is(err, errStatsUnavailable) {
				log.Printf("metrics: no reading for %s: %v", shortContainerID(c.ID), err)
			}
			continue
		}
		usage.Source = domain.LogSource{
			ApplicationID: c.Labels[labelApplication],
			DeploymentID:  c.Labels[labelDeployment],
			Service:       c.Labels[labelService],
		}
		out = append(out, usage)
	}
	return out, nil
}

// usageOf asks for a single reading. stream=false is what `docker stats
// --no-stream` does: the daemon returns one reading with the previous one
// alongside it, which is what makes a CPU percentage possible at all.
func (r *DockerRuntime) usageOf(ctx context.Context, containerID string) (domain.ContainerUsage, error) {
	stats, err := r.cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return domain.ContainerUsage{}, err
	}
	defer stats.Body.Close()

	usage, err := decodeUsage(stats.Body)
	if err != nil {
		return domain.ContainerUsage{}, err
	}
	usage.ContainerID = containerID
	return usage, nil
}

// errStatsUnavailable is the daemon having nothing to report: the
// container stopped between being listed and being read, and its stats
// stream closed without a reading.
var errStatsUnavailable = errors.New("no stats for this container")

func decodeUsage(body io.Reader) (domain.ContainerUsage, error) {
	var s types.StatsJSON
	if err := json.NewDecoder(body).Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return domain.ContainerUsage{}, errStatsUnavailable
		}
		return domain.ContainerUsage{}, fmt.Errorf("decode stats: %w", err)
	}
	return domain.ContainerUsage{
		CPUPercent:  cpuPercent(s),
		MemoryBytes: memoryUsed(s),
		MemoryLimit: int64(s.MemoryStats.Limit),
	}, nil
}

// cpuPercent is the formula `docker stats` itself prints: the container's
// CPU time over the interval against the host's total over the same
// interval, scaled by the number of CPUs — so 100% means one core's worth.
func cpuPercent(s types.StatsJSON) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	// A first reading has no previous one to compare against; reporting 0
	// is wrong in a way nobody can see, so the caller drops the sample.
	if cpuDelta <= 0 || systemDelta <= 0 || cpus == 0 {
		return 0
	}
	return cpuDelta / systemDelta * cpus * 100
}

// memoryUsed is what the container actually holds: the kernel counts page
// cache it would drop under pressure as "usage", and `docker stats`
// subtracts it. cgroup v2 calls that inactive_file; v1 called it cache.
func memoryUsed(s types.StatsJSON) int64 {
	used := int64(s.MemoryStats.Usage)
	for _, key := range []string{"inactive_file", "cache"} {
		if v, ok := s.MemoryStats.Stats[key]; ok {
			used -= int64(v)
			break
		}
	}
	if used < 0 {
		return 0
	}
	return used
}
