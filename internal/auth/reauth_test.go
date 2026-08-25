package auth

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"golang.org/x/oauth2"
)

func TestClassifyRefreshError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("dial tcp: i/o timeout"), false},
		{"revoked grant", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"bad client creds", &oauth2.RetrieveError{ErrorCode: "invalid_client"}, true},
		{"unauthorized client", &oauth2.RetrieveError{ErrorCode: "unauthorized_client"}, true},
		{"access denied", &oauth2.RetrieveError{ErrorCode: "access_denied"}, true},
		{
			"400 without an error code",
			&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadRequest}},
			true,
		},
		{
			"401 without an error code",
			&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}},
			true,
		},
		{
			"google 500 is transient",
			&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
			false,
		},
		{
			"rate limited is transient",
			&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusTooManyRequests}},
			false,
		},
		{
			"unknown error code with no response is transient",
			&oauth2.RetrieveError{ErrorCode: "temporarily_unavailable"},
			false,
		},
		{
			"wrapped revoked grant",
			fmt.Errorf("refreshing token: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}),
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyRefreshError(tc.err)
			if got.Permanent != tc.want {
				t.Fatalf("Permanent = %v, want %v", got.Permanent, tc.want)
			}
			if got.Permanent && got.Reason == "" {
				t.Fatal("permanent failure must carry a reason for the UI")
			}
			if !got.Permanent && got.Reason != "" {
				t.Fatalf("transient failure must not carry a reason, got %q", got.Reason)
			}
		})
	}
}
