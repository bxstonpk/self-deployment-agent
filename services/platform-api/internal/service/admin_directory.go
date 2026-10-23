package service

import (
	"context"
	"log"

	"platform-api/internal/domain"
)

type UserByEmail interface {
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

// EmailAdminDirectory resolves PLATFORM_ADMIN_EMAILS to accounts. An
// address with no account yet is skipped until its owner registers.
type EmailAdminDirectory struct {
	users  UserByEmail
	emails []string
}

func NewEmailAdminDirectory(users UserByEmail, emails []string) *EmailAdminDirectory {
	return &EmailAdminDirectory{users: users, emails: emails}
}

func (d *EmailAdminDirectory) Emails() []string { return d.emails }

func (d *EmailAdminDirectory) AdminUserIDs(ctx context.Context) []string {
	ids := []string{}
	for _, email := range d.emails {
		u, err := d.users.GetByEmail(ctx, email)
		if err != nil {
			log.Printf("admins: %s has no account yet", email)
			continue
		}
		ids = append(ids, u.ID)
	}
	return ids
}
