package httpapi

import (
	"errors"
	"net/http"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/hostinfo"
	"platform-api/internal/service"
)

// AdminHandler serves the platform administrators' dashboard.
type AdminHandler struct {
	svc       *service.AdminOverviewService
	publicURL string
}

func NewAdminHandler(svc *service.AdminOverviewService, publicBaseURL string) *AdminHandler {
	return &AdminHandler{svc: svc, publicURL: publicBaseURL}
}

type adminApplicationResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	Department   string  `json:"department"`
	OwnerEmail   string  `json:"owner_email"`
	Environments string  `json:"environments"`
	Instances    int     `json:"instances"`
	CPUPercent   float64 `json:"cpu_percent"`
	MemoryBytes  int64   `json:"memory_bytes"`
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	LatencyMsAvg float64 `json:"latency_ms_avg"`
	URL          string  `json:"url"`
}

type trafficPointResponse struct {
	Minute   time.Time `json:"minute"`
	Requests int64     `json:"requests"`
	Errors   int64     `json:"errors"`
}

type adminOverviewResponse struct {
	GeneratedAt          time.Time                  `json:"generated_at"`
	Host                 hostinfo.Status            `json:"host"`
	HostHistory          []hostinfo.Point           `json:"host_history"`
	DiskUsedPercent      *float64                   `json:"disk_used_percent"`
	Traffic              []trafficPointResponse     `json:"traffic"`
	TrafficWindowSeconds int                        `json:"traffic_window_seconds"`
	Applications         []adminApplicationResponse `json:"applications"`
}

// Overview is GET /admin/overview.
func (h *AdminHandler) Overview(w http.ResponseWriter, r *http.Request) {
	user, _, ok := caller(w, r)
	if !ok {
		return
	}
	o, err := h.svc.Overview(r.Context(), user)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			writeError(w, http.StatusForbidden, "forbidden", "only platform administrators can see the dashboard")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	resp := adminOverviewResponse{
		GeneratedAt: o.GeneratedAt, Host: o.Host, HostHistory: o.HostHistory, DiskUsedPercent: o.DiskUsedPercent,
		TrafficWindowSeconds: int(o.TrafficWindow.Seconds()),
		Traffic:              make([]trafficPointResponse, 0, len(o.Traffic)),
		Applications:         make([]adminApplicationResponse, 0, len(o.Applications)),
	}
	if resp.HostHistory == nil {
		resp.HostHistory = []hostinfo.Point{}
	}
	for _, p := range o.Traffic {
		resp.Traffic = append(resp.Traffic, trafficPointResponse{Minute: p.Minute, Requests: p.Requests, Errors: p.Errors})
	}
	for _, a := range o.Applications {
		url := ""
		if a.Instances > 0 || a.Environments != "" {
			url = serviceURL(h.publicURL, a.Name, "frontend", true)
		}
		resp.Applications = append(resp.Applications, adminApplicationResponse{
			ID: a.ID, Name: a.Name, Status: a.Status, Department: a.Department, OwnerEmail: a.OwnerEmail,
			Environments: a.Environments, Instances: a.Instances, CPUPercent: a.CPUPercent, MemoryBytes: a.MemoryBytes,
			Requests: a.Requests, Errors: a.Errors, LatencyMsAvg: a.LatencyMsAvg, URL: url,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
