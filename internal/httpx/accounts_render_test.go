package httpx

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ryakel/skulid/internal/db"
)

// accountRowsFor builds the page's view model for accounts with no calendars.
// Shared with the other Accounts-page render tests.
func accountRowsFor(accounts ...db.Account) []accountRow {
	rows := make([]accountRow, 0, len(accounts))
	for _, a := range accounts {
		rows = append(rows, newAccountRow(a, nil))
	}
	return rows
}

func renderAccountsRows(t *testing.T, rows []accountRow) string {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var buf bytes.Buffer
	err = r.Render(&buf, "accounts", map[string]any{
		"Title":    "Accounts",
		"Features": map[string]bool{"Assistant": false},
		"Version":  "test",
		"Rows":     rows,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func twoAccounts() []db.Account {
	return []db.Account{
		{ID: 1, Email: "one@example.com", CreatedAt: time.Now()},
		{ID: 2, Email: "two@example.com", CreatedAt: time.Now()},
	}
}

// One header at the top of a single long table is off-screen by the second
// account, which is the whole reason each account now carries its own.
func TestAccountsPageRepeatsTheHeaderPerAccount(t *testing.T) {
	accounts := twoAccounts()
	out := renderAccountsRows(t, accountRowsFor(accounts...))

	if got := strings.Count(out, "<th>Connected</th>"); got != len(accounts) {
		t.Errorf("rendered %d column headers, want one per account (%d)", got, len(accounts))
	}
	if got := strings.Count(out, `class="account-card"`); got != len(accounts) {
		t.Errorf("rendered %d account cards, want %d", got, len(accounts))
	}
}

func TestAccountsPageSplitsHiddenCalendarsIntoTheirOwnSection(t *testing.T) {
	accounts := []db.Account{{ID: 1, Email: "one@example.com", CreatedAt: time.Now()}}
	visible := db.Calendar{ID: 10, AccountID: 1, Summary: "Work", GoogleCalendarID: "work@x", Enabled: true}
	buried := db.Calendar{ID: 11, AccountID: 1, Summary: "Holidays", GoogleCalendarID: "hol@x", Hidden: true}

	out := renderAccountsRows(t, []accountRow{newAccountRow(accounts[0], []db.Calendar{visible, buried})})

	if !strings.Contains(out, "Hidden · 1") {
		t.Error("want a hidden disclosure naming how many are in it")
	}
	// The count in the account row stays the true total; the hidden ones are
	// called out separately rather than subtracted.
	if !strings.Contains(out, "1 hidden") {
		t.Error("the account row should say how many calendars are hidden")
	}
	// A hidden calendar must stay reachable, not vanish.
	if !strings.Contains(out, "Holidays") {
		t.Error("a hidden calendar must still be rendered inside the disclosure")
	}
	if !strings.Contains(out, `action="/calendars/11/hidden"`) {
		t.Error("a hidden calendar needs its unhide control")
	}
	if !strings.Contains(out, `action="/calendars/10/hidden"`) {
		t.Error("a visible calendar needs its hide control")
	}
}

// Hiding is cosmetic. A hidden calendar that is still enabled keeps syncing,
// and the UI has to say so or the owner will think they turned it off.
func TestAccountsPageMarksAHiddenButStillSyncingCalendar(t *testing.T) {
	accounts := []db.Account{{ID: 1, Email: "one@example.com", CreatedAt: time.Now()}}
	live := db.Calendar{ID: 12, AccountID: 1, Summary: "Work", GoogleCalendarID: "w@x", Enabled: true, Hidden: true}

	out := renderAccountsRows(t, []accountRow{newAccountRow(accounts[0], []db.Calendar{live})})
	if !strings.Contains(out, "still syncing") {
		t.Error("an enabled calendar that is hidden must be marked as still syncing")
	}
	if !strings.Contains(out, "Every calendar on this account is hidden") {
		t.Error("an account whose calendars are all hidden should say so, not look empty")
	}
}
