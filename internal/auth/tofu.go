package auth

import (
	"context"
	"errors"

	"github.com/ryakel/skulid/internal/db"
)

var ErrOwnerMismatch = errors.New("owner mismatch")

type TOFU struct {
	settings *db.SettingRepo
}

func NewTOFU(settings *db.SettingRepo) *TOFU { return &TOFU{settings: settings} }

// Claim records the first successful login as the instance owner. Subsequent
// logins must match the recorded owner — anything else returns ErrOwnerMismatch.
func (t *TOFU) Claim(ctx context.Context, googleSub, email string) error {
	storedSub, hasSub, err := t.settings.Get(ctx, db.SettingOwnerGoogleSub)
	if err != nil {
		return err
	}
	if !hasSub {
		if err := t.settings.Set(ctx, db.SettingOwnerGoogleSub, googleSub); err != nil {
			return err
		}
		if err := t.settings.Set(ctx, db.SettingOwnerEmail, email); err != nil {
			return err
		}
		return nil
	}
	if storedSub != googleSub {
		return ErrOwnerMismatch
	}
	// Owner already matches — refresh the email in case it has changed.
	if err := t.settings.Set(ctx, db.SettingOwnerEmail, email); err != nil {
		return err
	}
	return nil
}

// VerifyOwner checks that the active session belongs to the recorded owner.
func (t *TOFU) VerifyOwner(ctx context.Context, googleSub string) (bool, error) {
	storedSub, ok, err := t.settings.Get(ctx, db.SettingOwnerGoogleSub)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return storedSub == googleSub, nil
}

func (t *TOFU) OwnerEmail(ctx context.Context) (string, error) {
	v, _, err := t.settings.Get(ctx, db.SettingOwnerEmail)
	return v, err
}
