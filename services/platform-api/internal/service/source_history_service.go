// Source history: every build's kept source (internal/sourcestore), for the
// people responsible for an application to read at any time, not only while
// a deployment waits for approval. The platform owner, 2026-11-06: after
// approving, the files could no longer be found in the Portal, and a dev
// copy's builds (which never wait for approval) could not be read there at
// all.
//
// An application and its dev copy are read together: the history of
// "leave" is the builds of leave and of leave-dev, newest first.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"platform-api/internal/domain"
)

// SourceBuildLister reads builds.
type SourceBuildLister interface {
	ListForApplication(ctx context.Context, applicationID string) ([]domain.Build, error)
	GetByID(ctx context.Context, buildID string) (domain.Build, error)
}

// SourceDeploymentLister reads which deployments used a build.
type SourceDeploymentLister interface {
	ListForApplication(ctx context.Context, applicationID string) ([]domain.Deployment, error)
}

// SourceArchiveReader is the kept source store.
type SourceArchiveReader interface {
	Open(applicationID, buildID string) (io.ReadCloser, int64, error)
	Size(applicationID, buildID string) (int64, error)
}

// SourceCopies says which application is a dev copy of which.
type SourceCopies interface {
	Live(ctx context.Context, parentID string) (domain.DevCopy, error)
	Of(ctx context.Context, applicationID string) (domain.DevCopy, error)
}

// SourceApps reads applications.
type SourceApps interface {
	GetByID(ctx context.Context, id string) (domain.Application, error)
}

// SourceOwners reads who owns an application.
type SourceOwners interface {
	ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error)
}

// SourceUsers resolves who started a build.
type SourceUsers interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

type SourceHistoryService struct {
	apps        SourceApps
	owners      SourceOwners
	builds      SourceBuildLister
	deployments SourceDeploymentLister
	sources     SourceArchiveReader
	copies      SourceCopies
	users       SourceUsers
	admins      AdminChecker
	isApprover  func(email string) bool
}

func NewSourceHistoryService(apps SourceApps, owners SourceOwners, builds SourceBuildLister,
	deployments SourceDeploymentLister, sources SourceArchiveReader, copies SourceCopies, users SourceUsers,
	admins AdminChecker, isApprover func(email string) bool) *SourceHistoryService {
	return &SourceHistoryService{apps: apps, owners: owners, builds: builds, deployments: deployments, sources: sources,
		copies: copies, users: users, admins: admins, isApprover: isApprover}
}

// SourceDeployment is one deployment made from a build.
type SourceDeployment struct {
	ID          string
	Environment domain.Environment
	Status      domain.DeploymentStatus
}

// SourceEntry is one build and its kept source.
type SourceEntry struct {
	Build           domain.Build
	ApplicationName string
	DevCopy         bool   // the build is of the dev copy
	TriggeredBy     string // email, or the user id if the account can't be read
	Kept            bool   // false for a build from before sources were kept, or one whose file is gone
	SourceBytes     int64
	Deployments     []SourceDeployment
}

// family is the application and its dev copy, the application first.
type familyMember struct {
	app     domain.Application
	devCopy bool
}

func (s *SourceHistoryService) family(ctx context.Context, applicationID string) ([]familyMember, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	parent := app
	copyOf, err := s.copies.Of(ctx, applicationID)
	switch {
	case err == nil:
		if parent, err = s.apps.GetByID(ctx, copyOf.ParentID); err != nil {
			return nil, err
		}
	case !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}
	members := []familyMember{{app: parent}}
	if parent.ID != app.ID {
		// Asked from a dev copy: that copy, even if it has ended.
		return append(members, familyMember{app: app, devCopy: true}), nil
	}
	live, err := s.copies.Live(ctx, parent.ID)
	switch {
	case err == nil:
		copyApp, err := s.apps.GetByID(ctx, live.ApplicationID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			members = append(members, familyMember{app: copyApp, devCopy: true})
		}
	case !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}
	return members, nil
}

// authorize: an active owner of the application or of its dev copy, a
// platform administrator, or an approver.
func (s *SourceHistoryService) authorize(ctx context.Context, caller domain.User, members []familyMember) error {
	if s.admins != nil && s.admins.IsAdmin(caller.Email) {
		return nil
	}
	if s.isApprover != nil && s.isApprover(caller.Email) {
		return nil
	}
	for _, m := range members {
		owners, err := s.owners.ListForApplication(ctx, m.app.ID)
		if err != nil {
			return err
		}
		for _, o := range owners {
			if o.UserID == caller.ID && o.Status == "active" {
				return nil
			}
		}
	}
	return domain.ErrUnauthorized
}

// List is every build of the application and its dev copy, newest first.
func (s *SourceHistoryService) List(ctx context.Context, caller domain.User, applicationID string) ([]SourceEntry, error) {
	members, err := s.family(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, caller, members); err != nil {
		return nil, err
	}
	emails := map[string]string{}
	out := []SourceEntry{}
	for _, m := range members {
		builds, err := s.builds.ListForApplication(ctx, m.app.ID)
		if err != nil {
			return nil, err
		}
		deployments, err := s.deployments.ListForApplication(ctx, m.app.ID)
		if err != nil {
			return nil, err
		}
		byBuild := map[string][]SourceDeployment{}
		for _, d := range deployments {
			byBuild[d.BuildID] = append(byBuild[d.BuildID], SourceDeployment{ID: d.ID, Environment: d.Environment, Status: d.Status})
		}
		for _, b := range builds {
			e := SourceEntry{Build: b, ApplicationName: m.app.Name, DevCopy: m.devCopy, Deployments: byBuild[b.ID]}
			if size, err := s.sources.Size(m.app.ID, b.ID); err == nil {
				e.Kept, e.SourceBytes = true, size
			}
			e.TriggeredBy = s.email(ctx, emails, b.TriggeredBy)
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Build.StartedAt.After(out[j].Build.StartedAt) })
	return out, nil
}

func (s *SourceHistoryService) email(ctx context.Context, cache map[string]string, userID string) string {
	if e, ok := cache[userID]; ok {
		return e
	}
	e := userID
	if u, err := s.users.GetByID(ctx, userID); err == nil {
		e = u.Email
	}
	cache[userID] = e
	return e
}

// Source is one build's kept archive, for a build of the application or of
// its dev copy.
func (s *SourceHistoryService) Source(ctx context.Context, caller domain.User, applicationID, buildID string) (io.ReadCloser, int64, string, error) {
	members, err := s.family(ctx, applicationID)
	if err != nil {
		return nil, 0, "", err
	}
	if err := s.authorize(ctx, caller, members); err != nil {
		return nil, 0, "", err
	}
	build, err := s.builds.GetByID(ctx, buildID)
	if err != nil {
		return nil, 0, "", err
	}
	for _, m := range members {
		if m.app.ID != build.ApplicationID {
			continue
		}
		f, size, err := s.sources.Open(m.app.ID, build.ID)
		if err != nil {
			return nil, 0, "", domain.ErrNotFound
		}
		short := build.ID
		if len(short) > 8 {
			short = short[:8]
		}
		return f, size, fmt.Sprintf("%s-%s.tar.gz", m.app.Name, short), nil
	}
	// A build of some other application: not found here.
	return nil, 0, "", domain.ErrNotFound
}
