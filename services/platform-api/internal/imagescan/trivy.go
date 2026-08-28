// Package imagescan is the real implementation of FR-041 (Image Scan
// Gate): it runs Trivy (https://github.com/aquasecurity/trivy) — a widely
// used open-source scanner — as a sibling container on the same Docker
// daemon the Build Engine builds into, scanning a just-built local image
// directly (no registry round-trip needed).
//
// Policy note: FR-041 says the blocking severity threshold is owned by
// security policy, not this document. In the absence of a published
// threshold (docs/17_Decision_Log.md has no DEC item for this yet — worth
// adding), this implementation blocks on any CRITICAL-severity finding,
// the most defensible default and the one most scanners ship with. This
// should be revisited once Security Administrator policy is actually
// published, per FR-041's own business rule.
package imagescan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"platform-api/internal/domain"
)

// trivyCacheVolume persists Trivy's vulnerability database across scans —
// without it, every scan would re-download the full DB (~50s+ the first
// time, confirmed by hand before writing this).
const trivyCacheVolume = "platform-trivy-cache"

const trivyImage = "aquasec/trivy:latest"

type TrivyScanner struct {
	cli *client.Client
}

func NewTrivyScanner(cli *client.Client) *TrivyScanner {
	return &TrivyScanner{cli: cli}
}

func (s *TrivyScanner) Scan(ctx context.Context, imageRef string) (domain.ScanReport, error) {
	cfg := &container.Config{
		Image: trivyImage,
		Cmd:   []string{"image", "--format", "json", "--quiet", "--scanners", "vuln", imageRef},
	}
	hostCfg := &container.HostConfig{
		Binds: []string{"/var/run/docker.sock:/var/run/docker.sock"},
		Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: trivyCacheVolume, Target: "/root/.cache/trivy"},
		},
	}

	created, err := s.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return domain.ScanReport{}, fmt.Errorf("create scan container: %w", err)
	}
	defer func() { _ = s.cli.ContainerRemove(ctx, created.ID, types.ContainerRemoveOptions{Force: true}) }()

	if err := s.cli.ContainerStart(ctx, created.ID, types.ContainerStartOptions{}); err != nil {
		return domain.ScanReport{}, fmt.Errorf("start scan container: %w", err)
	}

	statusCh, errCh := s.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return domain.ScanReport{}, fmt.Errorf("wait for scan container: %w", err)
		}
	case <-statusCh:
	}

	logs, err := s.cli.ContainerLogs(ctx, created.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return domain.ScanReport{}, fmt.Errorf("read scan output: %w", err)
	}
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return domain.ScanReport{}, fmt.Errorf("demux scan output: %w", err)
	}

	report, err := parseTrivyReport(imageRef, stdout.Bytes())
	if err != nil {
		return domain.ScanReport{}, fmt.Errorf("parse scan report (stderr: %s): %w", stderr.String(), err)
	}
	return report, nil
}

func parseTrivyReport(imageRef string, raw []byte) (domain.ScanReport, error) {
	var parsed struct {
		Results []struct {
			Vulnerabilities []struct {
				VulnerabilityID string `json:"VulnerabilityID"`
				PkgName         string `json:"PkgName"`
				Severity        string `json:"Severity"`
				Title           string `json:"Title"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return domain.ScanReport{}, err
	}

	report := domain.ScanReport{ImageRef: imageRef}
	for _, result := range parsed.Results {
		for _, v := range result.Vulnerabilities {
			switch v.Severity {
			case "CRITICAL":
				report.CriticalCount++
			case "HIGH":
				report.HighCount++
			}
			if (v.Severity == "CRITICAL" || v.Severity == "HIGH") && len(report.TopFindings) < 10 {
				report.TopFindings = append(report.TopFindings, domain.ScanFinding{
					Severity: v.Severity, VulnerabilityID: v.VulnerabilityID,
					Package: v.PkgName, Title: v.Title,
				})
			}
		}
	}
	report.Passed = report.CriticalCount == 0
	return report, nil
}
