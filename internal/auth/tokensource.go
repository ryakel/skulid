package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/ryakel/skulid/internal/crypto"
	"github.com/ryakel/skulid/internal/db"
)

// AccountTokenSource builds an oauth2.TokenSource backed by a stored,
// encrypted refresh token. Whenever the access token is refreshed it
// persists the new value back to the database.
type AccountTokenSource struct {
	provider  *OAuthProvider
	sealer    *crypto.Sealer
	accounts  *db.AccountRepo
	accountID int64

	mu      sync.Mutex
	current *oauth2.Token
}

func NewAccountTokenSource(p *OAuthProvider, s *crypto.Sealer, accounts *db.AccountRepo, accountID int64) *AccountTokenSource {
	return &AccountTokenSource{provider: p, sealer: s, accounts: accounts, accountID: accountID}
}

func (a *AccountTokenSource) Token() (*oauth2.Token, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ctx := context.Background()
	acct, err := a.accounts.Get(ctx, a.accountID)
	if err != nil {
		return nil, err
	}
	refresh, err := a.sealer.Open(acct.RefreshTokenSealed)
	if err != nil {
		return nil, err
	}
	tok := &oauth2.Token{RefreshToken: refresh}
	if acct.AccessTokenSealed != "" && acct.AccessTokenExpiresAt != nil {
		access, err := a.sealer.Open(acct.AccessTokenSealed)
		if err == nil {
			tok.AccessToken = access
			tok.Expiry = *acct.AccessTokenExpiresAt
		}
	}
	src := a.provider.Config().TokenSource(ctx, tok)
	fresh, err := src.Token()
	if err != nil {
		// A revoked or expired grant never recovers on its own. Record it so
		// the UI can ask for a reconnect instead of letting every background
		// job fail into the log forever.
		if failure := ClassifyRefreshError(err); failure.Permanent {
			if markErr := a.accounts.MarkNeedsReauth(ctx, a.accountID, failure.Reason); markErr != nil {
				return nil, fmt.Errorf("refresh failed (%w) and flagging the account failed: %v", err, markErr)
			}
			return nil, fmt.Errorf("account %d needs reauthorization: %w", a.accountID, err)
		}
		return nil, err
	}
	if a.current == nil || fresh.AccessToken != a.current.AccessToken {
		// Persist the refreshed access token (and refresh token, if rotated).
		sealedAccess, err := a.sealer.Seal(fresh.AccessToken)
		if err != nil {
			return nil, err
		}
		if err := a.accounts.UpdateAccessToken(ctx, a.accountID, sealedAccess, fresh.Expiry); err != nil {
			return nil, err
		}
		if fresh.RefreshToken != "" && fresh.RefreshToken != refresh {
			sealedRefresh, err := a.sealer.Seal(fresh.RefreshToken)
			if err != nil {
				return nil, err
			}
			if err := a.accounts.UpdateRefreshToken(ctx, a.accountID, sealedRefresh); err != nil {
				return nil, err
			}
		}
	}
	a.current = fresh

	// The refresh worked, so any previously recorded lockout is stale.
	if acct.NeedsReauth {
		if err := a.accounts.ClearNeedsReauth(ctx, a.accountID); err != nil {
			return nil, err
		}
	}

	// Defensive: never hand back an already-expired token.
	if !fresh.Expiry.IsZero() && time.Now().After(fresh.Expiry) {
		return nil, fmt.Errorf("account %d: refreshed token is already expired at %s", a.accountID, fresh.Expiry)
	}
	return fresh, nil
}
